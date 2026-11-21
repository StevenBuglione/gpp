package javaport

import (
	"fmt"
	"go/token"
	"slices"
	"strings"
)

type javaObjectClass struct {
	file         SourceFile
	declaration  Occurrence
	parent       *javaObjectClass
	fields       []Occurrence
	initializers []Occurrence
	constructors []*javaObjectMethod
	methods      []*javaObjectMethod
	virtual      map[string]*javaObjectMethod
	embed        bool
	checksErrors bool
}

type javaObjectMethod struct {
	declaration Occurrence
	class       *javaObjectClass
	goName      string
	internal    string
	public      bool
	slot        *javaObjectMethod
	overridden  bool
}

type javaObjectField struct {
	declaration Occurrence
	class       *javaObjectClass
}

type javaObjectProgram struct {
	classes map[string]*javaObjectClass
	methods map[string]*javaObjectMethod
	fields  map[string]javaObjectField
	layout  []*javaObjectClass
	pending map[string]bool
	exports map[string]bool
	runtime bool
}

func emitJavaObjects(report Report) (string, error) {
	packageName, err := emissionPackage(report)
	if err != nil {
		return "", err
	}
	program := javaObjectProgram{
		classes: map[string]*javaObjectClass{}, methods: map[string]*javaObjectMethod{},
		fields: map[string]javaObjectField{}, pending: map[string]bool{}, exports: map[string]bool{},
	}
	var classes []*javaObjectClass
	for _, file := range report.Files {
		if file.Module != nil || len(file.Declarations) == 0 {
			return "", fmt.Errorf("%s: object emission requires a Java source file with classes", file.Path)
		}
		for _, declaration := range file.Declarations {
			if declaration.Owner != "" || declaration.Kind != "CLASS" {
				continue
			}
			if !token.IsExported(declaration.Name) || !token.IsIdentifier(declaration.Name) ||
				!hasOnlyModifiers(declaration, "PUBLIC", "FINAL") || !slices.Contains(declaration.Modifiers, "PUBLIC") ||
				!onlySourceAnnotations(declaration) || declaration.HasTypeParams || len(declaration.Supertypes) > 1 {
				return "", unsupportedSource(file, declaration, "expected a public non-generic class with at most one superclass")
			}
			if program.exports[declaration.Name] {
				return "", unsupportedSource(file, declaration, "duplicate Go class name")
			}
			class := &javaObjectClass{file: file, declaration: declaration}
			classes = append(classes, class)
			program.classes[declaration.Symbol] = class
			program.exports[declaration.Name] = true
		}
	}
	for _, file := range report.Files {
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
				return "", unsupportedSource(file, declaration, "nested, annotated, or non-class declaration")
			}
			if err := program.addMember(class, declaration); err != nil {
				return "", unsupportedSource(file, declaration, err.Error())
			}
		}
	}
	for _, class := range classes {
		if err := program.layoutClass(class); err != nil {
			return "", err
		}
	}
	var source strings.Builder
	for _, class := range program.layout {
		if err := program.emitClass(&source, class); err != nil {
			return "", err
		}
	}
	imports := ""
	if program.runtime {
		if packageName == "gpp.java" {
			return "", fmt.Errorf("Java runtime import conflicts with source package %q", packageName)
		}
		imports = "\nimport javart \"gpp/java\"\n"
	}
	return "package " + packageName + "\n" + imports + source.String(), nil
}

func (program *javaObjectProgram) addMember(class *javaObjectClass, declaration Occurrence) error {
	switch declaration.Kind {
	case "BLOCK":
		if declaration.StaticInitializer || declaration.Body == nil {
			return fmt.Errorf("static or unsupported initializer block")
		}
		class.initializers = append(class.initializers, declaration)
	case "VARIABLE":
		if objectGoType(declaration.Type) == "" || !token.IsIdentifier(declaration.Name) {
			return fmt.Errorf("unsupported field name or type %s", declaration.Type)
		}
		if slices.Contains(declaration.Modifiers, "STATIC") {
			if declaration.Constant == nil || !hasOnlyModifiers(declaration, "STATIC", "FINAL", "PUBLIC", "PROTECTED", "PRIVATE") {
				return fmt.Errorf("requires a compile-time primitive or String constant for static fields")
			}
			if slices.Contains(declaration.Modifiers, "PUBLIC") {
				name := class.declaration.Name + declaration.Name
				if program.exports[name] {
					return fmt.Errorf("duplicate exported Go constant %s", name)
				}
				program.exports[name] = true
			}
		} else {
			if !hasOnlyModifiers(declaration, "PRIVATE", "PROTECTED", "FINAL") {
				return fmt.Errorf("unsupported instance field modifiers")
			}
			if declaration.Initializer != nil {
				class.initializers = append(class.initializers, declaration)
			}
			program.runtime = program.runtime || declaration.Type == javaStringType
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
		if objectGoType(parameter.Type) == "" {
			return fmt.Errorf("unsupported parameter type %s", parameter.Type)
		}
		program.runtime = program.runtime || parameter.Type == javaStringType
	}
	method := &javaObjectMethod{declaration: declaration, class: class, public: slices.Contains(declaration.Modifiers, "PUBLIC")}
	if declaration.Name == "<init>" {
		suffix := constructorSuffix(declaration.Parameters)
		method.goName = "New" + class.declaration.Name + suffix
		method.internal = "javaInit" + suffix
		if method.public {
			if program.exports[method.goName] {
				return fmt.Errorf("duplicate exported Go constructor %s", method.goName)
			}
			program.exports[method.goName] = true
			if method.hasErrorAdapter() {
				name := method.errorAdapterName()
				if program.exports[name] {
					return fmt.Errorf("duplicate Go constructor error adapter %s", name)
				}
				program.exports[name] = true
			}
		}
		class.constructors = append(class.constructors, method)
	} else {
		if !method.public && !slices.Contains(declaration.Modifiers, "PRIVATE") {
			return fmt.Errorf("package-private or protected methods require additional dispatch support")
		}
		if declaration.Result != "void" && objectGoType(declaration.Result) == "" {
			return fmt.Errorf("unsupported method result type %s", declaration.Result)
		}
		program.runtime = program.runtime || declaration.Result == javaStringType
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
	if len(declaration.Supertypes) == 1 {
		class.parent = program.classes[declaration.Supertypes[0]]
		if class.parent == nil {
			return unsupportedSource(class.file, declaration, "superclass must be included in the same emission")
		}
		if err := program.layoutClass(class.parent); err != nil {
			return err
		}
		class.parent.embed = true
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
	for ancestor := class.parent; ancestor != nil; ancestor = ancestor.parent {
		if method := class.virtual[objectViewName(ancestor)]; method != nil {
			return unsupportedSource(class.file, declaration, "Go ancestor view collides with method "+method.goName)
		}
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
		if name == javaStringType {
			name = "String"
		}
		types = append(types, exportMethodName(name))
	}
	if len(types) == 0 {
		return ""
	}
	return "With" + strings.Join(types, "And")
}

func methodSignatureKey(declaration Occurrence) string {
	return declaration.Name + "#" + constructorSuffix(declaration.Parameters)
}

func objectViewName(class *javaObjectClass) string {
	return "GppAs" + class.declaration.Name
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
	receiver := "this"
	for class != owner && class.parent != nil {
		class = class.parent
		receiver += ".javaBase" + class.declaration.Name
	}
	return receiver, class == owner
}
