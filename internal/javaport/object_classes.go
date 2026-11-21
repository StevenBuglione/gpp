package javaport

import (
	"fmt"
	"go/token"
	"slices"
	"strings"
)

type javaObjectClass struct {
	file                   SourceFile
	declaration            Occurrence
	storagePackage         string
	symbolPrefix           string
	parent                 *javaObjectClass
	fields                 []Occurrence
	initializers           []Occurrence
	constructors           []*javaObjectMethod
	methods                []*javaObjectMethod
	virtual                map[string]*javaObjectMethod
	embed                  bool
	checksErrors           bool
	closeable              bool
	crossPackageDescendant bool
}

type javaObjectMethod struct {
	declaration      Occurrence
	class            *javaObjectClass
	goName           string
	internal         string
	public           bool
	slot             *javaObjectMethod
	overridden       bool
	crossPackageInit bool
}

type javaObjectField struct {
	declaration Occurrence
	class       *javaObjectClass
}

type javaObjectProgram struct {
	classes  map[string]*javaObjectClass
	methods  map[string]*javaObjectMethod
	fields   map[string]javaObjectField
	layout   []*javaObjectClass
	pending  map[string]bool
	exports  map[string]map[string]bool
	pkg      string
	facadeOf string
	imports  map[string]string
	runtime  bool
}

func emitJavaObjects(report Report) (string, error) {
	packageName, err := emissionPackage(report)
	if err != nil {
		return "", err
	}
	program, err := prepareJavaObjects(report.Files)
	if err != nil {
		return "", err
	}
	unit, err := program.emitPackage(packageName)
	return unit.Source, err
}

func prepareJavaObjects(files []SourceFile) (*javaObjectProgram, error) {
	program := &javaObjectProgram{
		classes: map[string]*javaObjectClass{}, methods: map[string]*javaObjectMethod{},
		fields: map[string]javaObjectField{}, pending: map[string]bool{}, exports: map[string]map[string]bool{},
	}
	var classes []*javaObjectClass
	for _, file := range files {
		if file.Module != nil || len(file.Declarations) == 0 {
			return nil, fmt.Errorf("%s: object emission requires a Java source file with classes", file.Path)
		}
		if program.exports[file.Package] == nil {
			program.exports[file.Package] = map[string]bool{}
		}
		for _, declaration := range file.Declarations {
			if declaration.Owner != "" || declaration.Kind != "CLASS" {
				continue
			}
			if !token.IsExported(declaration.Name) || !token.IsIdentifier(declaration.Name) ||
				!hasOnlyModifiers(declaration, "PUBLIC", "FINAL") || !slices.Contains(declaration.Modifiers, "PUBLIC") ||
				!onlySourceAnnotations(declaration) || declaration.HasTypeParams {
				return nil, unsupportedSource(file, declaration, "expected a public non-generic class with at most one superclass")
			}
			if program.exports[file.Package][declaration.Name] {
				return nil, unsupportedSource(file, declaration, "duplicate Go class name")
			}
			class := &javaObjectClass{file: file, declaration: declaration, storagePackage: file.Package}
			classes = append(classes, class)
			program.classes[declaration.Symbol] = class
			program.exports[file.Package][declaration.Name] = true
		}
	}
	for _, file := range files {
		for _, declaration := range file.Declarations {
			if declaration.Kind == "CLASS" && declaration.Owner == "" {
				continue
			}
			owner := declaration.Owner
			if declaration.Kind == "BLOCK" {
				owner = declaration.InitializerOwner
			}
			class, ok := program.classes[owner]
			if !ok || !onlySourceAnnotations(declaration) {
				return nil, unsupportedSource(file, declaration, "nested, annotated, or non-class declaration")
			}
			if err := program.addMember(class, declaration); err != nil {
				return nil, unsupportedSource(file, declaration, err.Error())
			}
		}
	}
	for _, class := range classes {
		if err := program.layoutClass(class); err != nil {
			return nil, err
		}
	}
	for _, class := range program.layout {
		for ancestor := class.parent; ancestor != nil; ancestor = ancestor.parent {
			if ancestor.file.Package != class.file.Package {
				ancestor.crossPackageDescendant = true
			}
		}
		for _, constructor := range class.constructors {
			call := constructor.declaration.ConstructorCall
			if call == nil || call.Receiver != "super" {
				continue
			}
			if target := program.methods[call.Symbol]; target != nil && target.class.file.Package != class.file.Package {
				target.crossPackageInit = true
			}
		}
	}
	for _, class := range program.layout {
		for owner := class; owner != nil; owner = owner.parent {
			for _, constructor := range owner.constructors {
				if constructor.crossPackageInit && class.virtual[constructor.bridgeName("Init")] != nil {
					return nil, unsupportedSource(class.file, class.declaration, "Go method collides with "+constructor.bridgeName("Init"))
				}
			}
			if !owner.crossPackageDescendant {
				continue
			}
			for _, method := range owner.methods {
				if method.overridden && class.virtual[method.bridgeName("Dispatch")] != nil {
					return nil, unsupportedSource(class.file, class.declaration, "Go method collides with "+method.bridgeName("Dispatch"))
				}
				if method.public && (method.slot != method || method.overridden) && class.virtual[method.bridgeName("Body")] != nil {
					return nil, unsupportedSource(class.file, class.declaration, "Go method collides with "+method.bridgeName("Body"))
				}
			}
		}
	}
	return program, nil
}

func (program *javaObjectProgram) addMember(class *javaObjectClass, declaration Occurrence) error {
	switch declaration.Kind {
	case "BLOCK":
		if declaration.StaticInitializer || declaration.Body == nil {
			return fmt.Errorf("static or unsupported initializer block")
		}
		class.initializers = append(class.initializers, declaration)
	case "VARIABLE":
		if program.mappedObjectType(declaration.Type) == "" || !token.IsIdentifier(declaration.Name) {
			return fmt.Errorf("unsupported field name or type %s", declaration.Type)
		}
		if slices.Contains(declaration.Modifiers, "STATIC") {
			if declaration.Constant == nil || !hasOnlyModifiers(declaration, "STATIC", "FINAL", "PUBLIC", "PROTECTED", "PRIVATE") {
				return fmt.Errorf("requires a compile-time primitive or String constant for static fields")
			}
			if slices.Contains(declaration.Modifiers, "PUBLIC") {
				name := class.declaration.Name + declaration.Name
				exports := program.exports[class.file.Package]
				if exports[name] {
					return fmt.Errorf("duplicate exported Go constant %s", name)
				}
				exports[name] = true
			}
		} else {
			if !hasOnlyModifiers(declaration, "PRIVATE", "PROTECTED", "FINAL") {
				return fmt.Errorf("unsupported instance field modifiers")
			}
			if declaration.Initializer != nil {
				class.initializers = append(class.initializers, declaration)
			}
		}
		class.fields = append(class.fields, declaration)
		program.fields[declaration.Symbol] = javaObjectField{declaration, class}
	case "METHOD":
		return program.addMethod(class, declaration)
	default:
		return fmt.Errorf("unsupported class member %s", declaration.Kind)
	}
	return nil
}

func (program *javaObjectProgram) addMethod(class *javaObjectClass, declaration Occurrence) error {
	if declaration.HasTypeParams || declaration.Body == nil ||
		!hasOnlyModifiers(declaration, "PUBLIC", "PRIVATE", "PROTECTED", "FINAL") {
		return fmt.Errorf("unsupported constructor or method modifiers, body, or type parameters")
	}
	for _, parameter := range declaration.Parameters {
		if program.mappedObjectType(parameter.Type) == "" {
			return fmt.Errorf("unsupported parameter type %s", parameter.Type)
		}
	}
	method := &javaObjectMethod{declaration: declaration, class: class, public: slices.Contains(declaration.Modifiers, "PUBLIC")}
	if declaration.Name == "<init>" {
		suffix := constructorSuffix(declaration.Parameters)
		method.goName = "New" + class.declaration.Name + suffix
		method.internal = "javaInit" + suffix
		for _, previous := range class.constructors {
			if method.internal == previous.internal {
				return fmt.Errorf("colliding Go constructor suffix %q", suffix)
			}
		}
		if method.public {
			exports := program.exports[class.file.Package]
			if exports[method.goName] {
				return fmt.Errorf("duplicate exported Go constructor %s", method.goName)
			}
			exports[method.goName] = true
			if method.hasErrorAdapter() {
				name := method.errorAdapterName()
				if exports[name] {
					return fmt.Errorf("duplicate Go constructor error adapter %s", name)
				}
				exports[name] = true
			}
		}
		class.constructors = append(class.constructors, method)
	} else {
		if !method.public && !slices.Contains(declaration.Modifiers, "PRIVATE") {
			return fmt.Errorf("package-private or protected methods require additional dispatch support")
		}
		if declaration.Result != "void" && program.mappedObjectType(declaration.Result) == "" {
			return fmt.Errorf("unsupported method result type %s", declaration.Result)
		}
		method.goName = exportMethodName(declaration.Name)
		method.internal = "javaBody" + declaration.Name
		if !token.IsIdentifier(method.goName) || !token.IsExported(method.goName) {
			return fmt.Errorf("unsupported method name")
		}
		for _, previous := range class.methods {
			if method.goName == previous.goName {
				return fmt.Errorf("overloaded or colliding Go method %s", method.goName)
			}
		}
		class.methods = append(class.methods, method)
	}
	program.methods[declaration.Symbol] = method
	return nil
}

func (program *javaObjectProgram) layoutClass(class *javaObjectClass) error {
	if class.virtual != nil {
		return nil
	}
	declaration := class.declaration
	if program.pending[declaration.Symbol] {
		return unsupportedSource(class.file, declaration, "circular class hierarchy")
	}
	program.pending[declaration.Symbol] = true
	if len(class.constructors) == 0 {
		return unsupportedSource(class.file, declaration, "missing attributed Java constructor")
	}
	for _, supertype := range declaration.Supertypes {
		if supertype == javaAutoCloseable {
			class.closeable = true
			continue
		}
		if class.parent != nil || program.classes[supertype] == nil {
			return unsupportedSource(class.file, declaration, "unsupported Java supertype "+supertype)
		}
		class.parent = program.classes[supertype]
	}
	if class.parent != nil {
		if err := program.layoutClass(class.parent); err != nil {
			return err
		}
		if class.parent.file.Package == class.file.Package {
			class.parent.embed = true
		}
		class.closeable = class.closeable || class.parent.closeable
	}
	class.virtual = map[string]*javaObjectMethod{}
	if class.parent != nil {
		for name, implementation := range class.parent.virtual {
			class.virtual[name] = implementation
		}
	}
	for _, method := range class.methods {
		if !method.public {
			continue
		}
		method.slot = method
		if inherited := class.virtual[method.goName]; inherited != nil {
			if methodSignatureKey(method.declaration) != methodSignatureKey(inherited.declaration) {
				return unsupportedSource(class.file, method.declaration, "overloaded or colliding inherited Go method")
			}
			method.slot = inherited.slot
			method.slot.overridden = true
		}
		class.virtual[method.goName] = method
	}
	if class.closeable && class.virtual["GppAutoCloseable"] != nil {
		return unsupportedSource(class.file, declaration, "Go AutoCloseable marker collides with method GppAutoCloseable")
	}
	views := map[string]bool{}
	for ancestor := class.parent; ancestor != nil; ancestor = ancestor.parent {
		view := objectViewName(ancestor)
		if views[view] || class.virtual[view] != nil {
			return unsupportedSource(class.file, declaration, "colliding Go ancestor view "+view)
		}
		views[view] = true
	}
	for owner := class; owner != nil; owner = owner.parent {
		for _, method := range owner.methods {
			if method.hasErrorAdapter() && class.virtual[method.errorAdapterName()] != nil {
				return unsupportedSource(class.file, declaration, "Go error adapter collides with method "+method.errorAdapterName())
			}
		}
	}
	delete(program.pending, declaration.Symbol)
	program.layout = append(program.layout, class)
	return nil
}

func hasOnlyModifiers(declaration Occurrence, permitted ...string) bool {
	for _, modifier := range declaration.Modifiers {
		if !slices.Contains(permitted, modifier) {
			return false
		}
	}
	return true
}

func onlySourceAnnotations(declaration Occurrence) bool {
	for _, annotation := range declaration.Annotations {
		if annotation.Retention != "SOURCE" {
			return false
		}
	}
	return true
}

func constructorSuffix(parameters []Parameter) string {
	var types []string
	for _, parameter := range parameters {
		name := parameter.Type
		name = name[strings.LastIndex(name, ".")+1:]
		name = strings.ReplaceAll(name, "[]", "Array")
		types = append(types, exportMethodName(name))
	}
	if len(types) == 0 {
		return ""
	}
	return "With" + strings.Join(types, "And")
}

func methodSignatureKey(declaration Occurrence) string {
	var types []string
	for _, parameter := range declaration.Parameters {
		types = append(types, parameter.Type)
	}
	return declaration.Name + "(" + strings.Join(types, ",") + ")"
}

func (method *javaObjectMethod) allocatorName() string {
	if method.public {
		return method.goName
	}
	return "java" + method.goName
}

func objectViewName(class *javaObjectClass) string {
	return "GppAs" + class.declaration.Name
}

func (class *javaObjectClass) baseField() string {
	if class.file.Package != class.parent.file.Package {
		return "javaImportedBase" + class.storageName(class.declaration.Name)
	}
	return "javaBase" + class.parent.storageName(class.parent.declaration.Name)
}

func (class *javaObjectClass) storageName(name string) string {
	if class.symbolPrefix == "" {
		return name
	}
	if !token.IsExported(name) {
		return "java" + class.symbolPrefix + name
	}
	return class.symbolPrefix + name
}

func (method *javaObjectMethod) bridgeName(operation string) string {
	return "GppJava" + operation + "_" + method.class.declaration.Name + "_" + method.internal
}

func (method *javaObjectMethod) hasErrorAdapter() bool {
	return method.public && len(method.declaration.Thrown) != 0
}

func (method *javaObjectMethod) errorAdapterName() string {
	if method.declaration.Name == "<init>" {
		return "Gpp" + method.goName
	}
	return "GppCall" + method.goName
}

func (class *javaObjectClass) memberReceiver(owner *javaObjectClass) (string, bool) {
	if class == owner {
		return "this", true
	}
	for ancestor := class.parent; ancestor != nil; ancestor = ancestor.parent {
		if ancestor == owner {
			return "this." + objectViewName(owner) + "()", true
		}
	}
	return "", false
}
