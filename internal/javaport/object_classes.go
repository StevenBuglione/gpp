package javaport

import (
	"fmt"
	"go/token"
	"maps"
	"slices"
	"strings"
)

type javaObjectClass struct {
	file                   SourceFile
	declaration            Occurrence
	goName                 string
	storagePackage         string
	symbolPrefix           string
	parent                 *javaObjectClass
	enclosing              *javaObjectClass
	fields                 []Occurrence
	enumConstants          []Occurrence
	enumBase               *javaObjectClass
	initializers           []Occurrence
	staticInitializers     []Occurrence
	constructors           []*javaObjectMethod
	methods                []*javaObjectMethod
	staticMethods          []*javaObjectMethod
	virtual                map[string]*javaObjectMethod
	interfaces             map[string]*javaInterface
	embed                  bool
	checksErrors           bool
	closeable              bool
	hasAssertions          bool
	activeInitialization   bool
	crossPackageDescendant bool
}

type javaObjectMethod struct {
	declaration      Occurrence
	class            *javaObjectClass
	goName           string
	internal         string
	public           bool
	static           bool
	slot             *javaObjectMethod
	overridden       bool
	crossPackageInit bool
	defaultMethod    *javaInterfaceMethod
	recordImplicit   bool
	enumImplicit     bool
}

type javaObjectField struct {
	declaration Occurrence
	class       *javaObjectClass
}

type javaObjectProgram struct {
	classes           map[string]*javaObjectClass
	methods           map[string]*javaObjectMethod
	fields            map[string]javaObjectField
	layout            []*javaObjectClass
	pending           map[string]bool
	exports           map[string]map[string]bool
	pkg               string
	facadeOf          string
	imports           map[string]string
	runtime           bool
	primitiveLowering primitiveLowering
	interfaces        map[string]*javaInterface
	interfaceLayout   []*javaInterface
	interfaceMethods  map[string]*javaInterfaceMethod
	lambdaAdapters    map[string]*javaLambdaAdapter
	lambdaNext        int
	annotationTypes   map[string]*javaAnnotationType
	annotationMembers map[string][]Occurrence
	memberTypeNames   map[string]string
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
		interfaces: map[string]*javaInterface{}, interfaceMethods: map[string]*javaInterfaceMethod{},
		annotationTypes: map[string]*javaAnnotationType{}, annotationMembers: map[string][]Occurrence{},
	}
	var classes []*javaObjectClass
	members := map[string]javaMemberDeclaration{}
	permitted := map[string]bool{}
	for _, file := range files {
		for _, declaration := range file.Declarations {
			for _, subtype := range declaration.PermittedSubclasses {
				permitted[subtype] = true
			}
		}
	}
	for _, file := range files {
		if file.Module != nil || len(file.Declarations) == 0 {
			return nil, fmt.Errorf("%s: object emission requires a Java source file with classes", file.Path)
		}
		if program.exports[file.Package] == nil {
			program.exports[file.Package] = map[string]bool{}
		}
		for _, declaration := range file.Declarations {
			if declaration.Nesting == "LOCAL" || (declaration.Nesting == "ANONYMOUS" && declaration.Kind != "ENUM") {
				return nil, unsupportedSource(file, declaration, "local and anonymous Java classes are unsupported")
			}
			if declaration.Owner != "" {
				if declaration.Kind == "CLASS" || declaration.Kind == "RECORD" {
					members[declaration.Symbol] = javaMemberDeclaration{file, declaration}
				}
				continue
			}
			if declaration.Kind == "ANNOTATION_TYPE" {
				if !token.IsIdentifier(declaration.Name) {
					return nil, unsupportedSource(file, declaration, "unsupported Go annotation declaration name")
				}
				program.annotationTypes[declaration.Symbol] = &javaAnnotationType{file: file, declaration: declaration, storagePackage: file.Package}
				continue
			}
			declaration = eraseJavaObjectDeclaration(declaration)
			if declaration.Kind == "INTERFACE" {
				if err := program.addInterface(file, declaration, permitted[declaration.Symbol]); err != nil {
					return nil, unsupportedSource(file, declaration, err.Error())
				}
				continue
			}
			if declaration.Kind != "CLASS" && declaration.Kind != "RECORD" && declaration.Kind != "ENUM" {
				continue
			}
			var enumBase *javaObjectClass
			if declaration.Kind == "ENUM" && declaration.Name == "" {
				enumBase = program.enumVariantBase(declaration)
				if enumBase == nil {
					return nil, unsupportedSource(file, declaration, "unresolved Java enum constant class body")
				}
				declaration.Name = "javaEnumVariant_" + enumBase.declaration.Name + strings.TrimPrefix(declaration.Symbol, enumBase.declaration.Symbol+"$")
			}
			visible := enumBase != nil || (token.IsExported(declaration.Name) &&
				(slices.Contains(declaration.Modifiers, "PUBLIC") || permitted[declaration.Symbol] || javaIsSealed(declaration)))
			if !token.IsIdentifier(declaration.Name) || !visible ||
				!hasOnlyModifiers(declaration, "PUBLIC", "FINAL", "ABSTRACT", "SEALED", "NON_SEALED") {
				return nil, unsupportedSource(file, declaration, "expected a public class with at most one superclass")
			}
			if program.exports[file.Package][declaration.Name] {
				return nil, unsupportedSource(file, declaration, "duplicate Go class name")
			}
			class := &javaObjectClass{file: file, declaration: declaration, storagePackage: file.Package, enumBase: enumBase}
			classes = append(classes, class)
			program.classes[declaration.Symbol] = class
			program.exports[file.Package][declaration.Name] = true
		}
	}
	memberClasses, err := program.addMemberClasses(members)
	if err != nil {
		return nil, err
	}
	classes = append(classes, memberClasses...)
	for _, class := range classes {
		class.declaration = program.resolveMemberTypes(class.declaration)
	}
	if err := program.validateSealedTypes(); err != nil {
		return nil, err
	}
	for _, file := range files {
		for _, declaration := range file.Declarations {
			if program.classes[declaration.Symbol] != nil && (declaration.Kind == "CLASS" || declaration.Kind == "RECORD" || declaration.Kind == "ENUM") {
				continue
			}
			if (declaration.Kind == "CLASS" || declaration.Kind == "INTERFACE" || declaration.Kind == "RECORD" || declaration.Kind == "ENUM" || declaration.Kind == "ANNOTATION_TYPE") && declaration.Owner == "" {
				continue
			}
			owner := declaration.Owner
			if annotation := program.annotationTypes[owner]; annotation != nil {
				if declaration.Kind != "METHOD" || len(declaration.Parameters) != 0 || !token.IsIdentifier(declaration.Name) {
					return nil, unsupportedSource(file, declaration, "unsupported Java annotation type member")
				}
				annotation.members = append(annotation.members, declaration)
				continue
			}
			if declarationHasRuntimeAnnotations(declaration) {
				program.annotationMembers[owner] = append(program.annotationMembers[owner], declaration)
			}
			declaration = eraseJavaObjectDeclaration(declaration)
			if declaration.Kind == "BLOCK" {
				owner = declaration.InitializerOwner
			}
			if iface := program.interfaces[owner]; iface != nil {
				if err := program.addInterfaceMember(iface, program.resolveMemberTypes(declaration)); err != nil {
					return nil, unsupportedSource(file, declaration, err.Error())
				}
				continue
			}
			class, ok := program.classes[owner]
			if !ok {
				return nil, unsupportedSource(file, declaration, "nested or non-class declaration")
			}
			if err := program.addMember(class, program.resolveMemberTypes(declaration)); err != nil {
				return nil, unsupportedSource(file, declaration, err.Error())
			}
		}
	}
	for _, iface := range program.interfaceLayout {
		if err := program.layoutInterface(iface); err != nil {
			return nil, err
		}
	}
	for _, class := range classes {
		if class.declaration.Kind == "RECORD" {
			if err := program.completeRecord(class); err != nil {
				return nil, unsupportedSource(class.file, class.declaration, err.Error())
			}
		}
		if class.isEnum() {
			if err := program.completeEnum(class); err != nil {
				return nil, unsupportedSource(class.file, class.declaration, err.Error())
			}
		}
	}
	for _, class := range classes {
		class.findAssertions()
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
	return program, program.reserveAnnotationExports()
}

func (program *javaObjectProgram) addMember(class *javaObjectClass, declaration Occurrence) error {
	switch declaration.Kind {
	case "BLOCK":
		if declaration.Body == nil {
			return fmt.Errorf("unsupported initializer block")
		}
		if declaration.StaticInitializer {
			class.staticInitializers = append(class.staticInitializers, declaration)
		} else {
			class.initializers = append(class.initializers, declaration)
		}
	case "VARIABLE":
		if program.mappedObjectType(declaration.Type) == "" || (!declaration.EnumConstant && !token.IsIdentifier(declaration.Name)) {
			return fmt.Errorf("unsupported field name or type %s", declaration.Type)
		}
		if declaration.EnumConstant {
			if err := program.addEnumConstant(class, declaration); err != nil {
				return err
			}
		} else if slices.Contains(declaration.Modifiers, "STATIC") {
			if err := program.addStaticField(class, declaration); err != nil {
				return err
			}
		} else {
			if !hasOnlyModifiers(declaration, "PUBLIC", "PRIVATE", "PROTECTED", "FINAL") {
				return fmt.Errorf("unsupported instance field modifiers")
			}
			if declaration.Initializer != nil {
				class.initializers = append(class.initializers, declaration)
			}
		}
		class.fields = append(class.fields, declaration)
		program.fields[declaration.Symbol] = javaObjectField{declaration, class}
	case "METHOD":
		if slices.Contains(declaration.Modifiers, "STATIC") {
			return program.addStaticMethod(class, declaration)
		}
		return program.addMethod(class, declaration)
	default:
		return fmt.Errorf("unsupported class member %s", declaration.Kind)
	}
	return nil
}

func (program *javaObjectProgram) addMethod(class *javaObjectClass, declaration Occurrence) error {
	abstract := slices.Contains(declaration.Modifiers, "ABSTRACT")
	if (declaration.Body == nil) != abstract ||
		(abstract && (!class.isAbstract() || declaration.Name == "<init>")) ||
		!hasOnlyModifiers(declaration, "PUBLIC", "PRIVATE", "PROTECTED", "FINAL", "ABSTRACT") {
		return fmt.Errorf("unsupported constructor or method modifiers, body, or type parameters")
	}
	for _, parameter := range declaration.Parameters {
		if program.mappedObjectType(parameter.Type) == "" {
			return fmt.Errorf("unsupported parameter type %s", parameter.Type)
		}
	}
	method := &javaObjectMethod{declaration: declaration, class: class, public: slices.Contains(declaration.Modifiers, "PUBLIC")}
	if declaration.Name == "<init>" {
		if class.declaration.Kind == "RECORD" && declaration.VariableArity {
			return fmt.Errorf("variable-arity record constructors require Java varargs lowering")
		}
		suffix := constructorSuffix(declaration.Parameters)
		method.goName = "New" + class.goTypeName() + suffix
		method.internal = "javaInit" + suffix
		for _, previous := range class.constructors {
			if method.internal == previous.internal {
				return fmt.Errorf("colliding Go constructor suffix %q", suffix)
			}
		}
		if method.public && !class.isAbstract() {
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
	class.interfaces = map[string]*javaInterface{}
	for _, supertype := range declaration.Supertypes {
		if declaration.Kind == "RECORD" && supertype == javaRecordType {
			continue
		}
		if class.isEnum() && (supertype == javaEnumType || strings.HasPrefix(supertype, javaEnumType+"<")) {
			continue
		}
		if supertype == javaAutoCloseable {
			class.closeable = true
			continue
		}
		if iface := program.interfaces[supertype]; iface != nil {
			maps.Copy(class.interfaces, iface.supers)
			class.closeable = class.closeable || iface.closeable
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
		maps.Copy(class.interfaces, class.parent.interfaces)
	}
	if err := program.layoutStaticInitialization(class); err != nil {
		return unsupportedSource(class.file, declaration, err.Error())
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
		if err := class.addVirtualMethod(method); err != nil {
			return unsupportedSource(class.file, method.declaration, err.Error())
		}
	}
	if err := program.completeInterfaceMethods(class); err != nil {
		return unsupportedSource(class.file, declaration, err.Error())
	}
	if !class.isAbstract() {
		for _, name := range slices.Sorted(maps.Keys(class.virtual)) {
			method := class.virtual[name]
			if method.declaration.Body == nil && method.defaultMethod == nil {
				return unsupportedSource(class.file, declaration, "missing implementation of abstract Java method "+name)
			}
		}
	}
	if class.closeable && class.virtual["GppAutoCloseable"] != nil {
		return unsupportedSource(class.file, declaration, "Go AutoCloseable marker collides with method GppAutoCloseable")
	}
	if declaration.Kind == "RECORD" && class.virtual["GppJavaRecord"] != nil {
		return unsupportedSource(class.file, declaration, "Go record marker collides with method GppJavaRecord")
	}
	if class.hasEnumIdentity() {
		for _, name := range []string{"Name", "Ordinal", "GppJavaEnum"} {
			if class.virtual[name] != nil {
				return unsupportedSource(class.file, declaration, "Go enum bridge collides with method "+name)
			}
		}
		if method := class.virtual["ToString"]; method != nil && method.declaration.Name != "toString" {
			return unsupportedSource(class.file, declaration, "Go enum bridge collides with method ToString")
		}
	}
	for _, method := range []string{"GppJavaBindOwner", "GppJavaOwner", "GppJavaCanonicalObject", "GppJavaReferenceView",
		"GppJavaClassName", "GppJavaIdentityHash", "GppJavaString", "GppJavaHashCode", javaImplementsBridge} {
		if class.virtual[method] != nil {
			return unsupportedSource(class.file, declaration, "Go reference bridge collides with method "+method)
		}
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

func (class *javaObjectClass) addVirtualMethod(method *javaObjectMethod) error {
	method.slot = method
	if inherited := class.virtual[method.goName]; inherited != nil {
		if methodSignatureKey(method.declaration) != methodSignatureKey(inherited.declaration) {
			return fmt.Errorf("overloaded or colliding inherited Go method")
		}
		method.slot = inherited.slot
		method.slot.overridden = true
	}
	class.virtual[method.goName] = method
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

func constructorSuffix(parameters []Parameter) string {
	var types []string
	for _, parameter := range parameters {
		name := parameter.Type
		if primitive := wrapperPrimitive(name); primitive != "" {
			types = append(types, "Boxed"+wrapperNames[primitive])
			continue
		}
		name = name[strings.LastIndex(name, ".")+1:]
		name = strings.ReplaceAll(name, "$", "_")
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
	return "GppAs" + class.goTypeName()
}

func (class *javaObjectClass) baseField() string {
	if class.file.Package != class.parent.file.Package {
		return "javaImportedBase" + class.storageName(class.goTypeName())
	}
	return "javaBase" + class.parent.storageName(class.parent.goTypeName())
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
	return "GppJava" + operation + "_" + method.class.goTypeName() + "_" + method.internal
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
