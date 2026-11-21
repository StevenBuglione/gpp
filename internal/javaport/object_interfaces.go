package javaport

import (
	"fmt"
	"go/token"
	"maps"
	"slices"
	"strconv"
	"strings"
)

const javaImplementsBridge = "GppJavaImplementsInterface"

type javaInterface struct {
	file           SourceFile
	declaration    Occurrence
	storagePackage string
	symbolPrefix   string
	supers         map[string]*javaInterface
	methods        []*javaInterfaceMethod
	constants      []Occurrence
	closeable      bool
	checksErrors   bool
	hasAssertions  bool
}

type javaInterfaceMethod struct {
	owner       *javaInterface
	declaration Occurrence
	goName      string
	isDefault   bool
}

func (program *javaObjectProgram) addInterface(file SourceFile, declaration Occurrence) error {
	if !token.IsExported(declaration.Name) || !token.IsIdentifier(declaration.Name) ||
		!hasOnlyModifiers(declaration, "PUBLIC", "ABSTRACT") || !slices.Contains(declaration.Modifiers, "PUBLIC") ||
		!onlySourceAnnotations(declaration) || declaration.HasTypeParams {
		return fmt.Errorf("expected a public non-generic Java interface")
	}
	if program.exports[file.Package][declaration.Name] {
		return fmt.Errorf("duplicate Go interface name")
	}
	iface := &javaInterface{file: file, declaration: declaration, storagePackage: file.Package}
	program.interfaces[declaration.Symbol] = iface
	program.interfaceLayout = append(program.interfaceLayout, iface)
	program.exports[file.Package][declaration.Name] = true
	return nil
}

func (program *javaObjectProgram) addInterfaceMember(iface *javaInterface, declaration Occurrence) error {
	if !onlySourceAnnotations(declaration) {
		return fmt.Errorf("unsupported Java interface member annotations")
	}
	switch declaration.Kind {
	case "METHOD":
		abstract := slices.Contains(declaration.Modifiers, "ABSTRACT")
		isDefault := slices.Contains(declaration.Modifiers, "DEFAULT")
		if declaration.HasTypeParams || !hasOnlyModifiers(declaration, "PUBLIC", "ABSTRACT", "DEFAULT") ||
			abstract == isDefault || (isDefault && declaration.Body == nil) {
			return fmt.Errorf("expected an abstract or attributed default Java interface method")
		}
		method := &javaInterfaceMethod{owner: iface, declaration: declaration,
			goName: exportMethodName(declaration.Name), isDefault: isDefault}
		if !token.IsExported(method.goName) || !token.IsIdentifier(method.goName) {
			return fmt.Errorf("unsupported Go interface method name")
		}
		for _, previous := range iface.methods {
			if previous.goName == method.goName {
				return fmt.Errorf("overloaded or colliding Go interface method %s", method.goName)
			}
		}
		for _, parameter := range declaration.Parameters {
			if program.mappedObjectType(parameter.Type) == "" {
				return fmt.Errorf("unsupported interface parameter type %s", parameter.Type)
			}
		}
		if declaration.Result != "void" && program.mappedObjectType(declaration.Result) == "" {
			return fmt.Errorf("unsupported interface result type %s", declaration.Result)
		}
		if isDefault {
			if err := program.reserveInterfaceExport(iface, method.defaultName()); err != nil {
				return err
			}
		}
		iface.methods = append(iface.methods, method)
		iface.hasAssertions = iface.hasAssertions || declaration.Body.containsStatement(isJavaAssertion)
		program.interfaceMethods[declaration.Symbol] = method
	case "VARIABLE":
		if declaration.Constant == nil || !token.IsIdentifier(declaration.Name) ||
			(primitiveTypes[declaration.Type] == "" && declaration.Type != javaStringType) {
			return fmt.Errorf("unsupported non-constant Java interface field")
		}
		if err := program.reserveInterfaceExport(iface, iface.declaration.Name+declaration.Name); err != nil {
			return err
		}
		iface.constants = append(iface.constants, declaration)
	default:
		return fmt.Errorf("unsupported Java interface member %s", declaration.Kind)
	}
	return nil
}

func (program *javaObjectProgram) reserveInterfaceExport(iface *javaInterface, name string) error {
	exports := program.exports[iface.file.Package]
	if exports[name] {
		return fmt.Errorf("duplicate Go interface export %s", name)
	}
	exports[name] = true
	return nil
}

func (program *javaObjectProgram) layoutInterface(iface *javaInterface) error {
	if iface.supers != nil {
		return nil
	}
	declaration := iface.declaration
	if program.pending[declaration.Symbol] {
		return unsupportedSource(iface.file, declaration, "circular Java interface hierarchy")
	}
	program.pending[declaration.Symbol] = true
	supers := map[string]*javaInterface{declaration.Symbol: iface}
	for _, parent := range declaration.Supertypes {
		if parent == javaAutoCloseable {
			iface.closeable = true
			continue
		}
		supertype := program.interfaces[parent]
		if supertype == nil {
			return unsupportedSource(iface.file, declaration, "unsupported Java superinterface "+parent)
		}
		if err := program.layoutInterface(supertype); err != nil {
			return err
		}
		iface.closeable = iface.closeable || supertype.closeable
		maps.Copy(supers, supertype.supers)
	}
	iface.supers = supers
	delete(program.pending, declaration.Symbol)
	_, err := interfaceContracts(supers)
	if err != nil {
		return unsupportedSource(iface.file, declaration, err.Error())
	}
	return nil
}

func interfaceContracts(interfaces map[string]*javaInterface) (map[string][]*javaInterfaceMethod, error) {
	contracts := map[string][]*javaInterfaceMethod{}
	markers := interfaceMarkers(interfaces)
	for _, name := range slices.Sorted(maps.Keys(interfaces)) {
		for _, method := range interfaces[name].methods {
			if markers[method.goName] || method.goName == javaImplementsBridge ||
				method.goName == "GppJavaCanonicalObject" || method.goName == "GppJavaReferenceView" ||
				method.goName == "GppJavaOwner" || method.goName == "GppJavaBindOwner" {
				return nil, fmt.Errorf("Go interface method %s collides with a Java bridge", method.goName)
			}
			contract := contracts[method.goName]
			if len(contract) != 0 && !sameInterfaceSignature(contract[0].declaration, method.declaration) {
				return nil, fmt.Errorf("overloaded, covariant or colliding Java interface method %s", method.goName)
			}
			contracts[method.goName] = append(contract, method)
		}
	}
	return contracts, nil
}

func sameInterfaceSignature(left, right Occurrence) bool {
	return left.Result == right.Result && methodSignatureKey(left) == methodSignatureKey(right)
}

func interfaceMarkers(interfaces map[string]*javaInterface) map[string]bool {
	markers := map[string]bool{}
	for _, iface := range interfaces {
		if len(iface.declaration.Supertypes) != 0 {
			markers["Is"+iface.declaration.Name] = true
		}
	}
	return markers
}

func (program *javaObjectProgram) completeInterfaceMethods(class *javaObjectClass) error {
	contracts, err := interfaceContracts(class.interfaces)
	if err != nil {
		return err
	}
	for _, name := range slices.Sorted(maps.Keys(contracts)) {
		contract := contracts[name]
		implementation := class.virtual[name]
		if implementation != nil {
			if !sameInterfaceSignature(implementation.declaration, contract[0].declaration) {
				return fmt.Errorf("cannot map a covariant or overloaded Java interface implementation %s", name)
			}
			if implementation.defaultMethod == nil {
				continue
			}
		}
		selected := mostSpecificDefault(contract)
		if selected == nil {
			return fmt.Errorf("missing concrete Java interface method %s", name)
		}
		if implementation != nil && implementation.defaultMethod == selected {
			continue
		}
		for _, method := range class.methods {
			if method.goName == name {
				return fmt.Errorf("Go class method %s collides with a Java default method", name)
			}
		}
		method := &javaObjectMethod{class: class, declaration: selected.declaration,
			goName: name, internal: "javaBody" + selected.declaration.Name, public: true, defaultMethod: selected}
		if err := class.addVirtualMethod(method); err != nil {
			return err
		}
		class.methods = append(class.methods, method)
	}
	for _, marker := range slices.Sorted(maps.Keys(interfaceMarkers(class.interfaces))) {
		if class.virtual[marker] != nil {
			return fmt.Errorf("Go method %s collides with a Java interface marker", marker)
		}
	}
	return nil
}

func mostSpecificDefault(methods []*javaInterfaceMethod) *javaInterfaceMethod {
	var selected *javaInterfaceMethod
	for _, method := range methods {
		if !method.isDefault {
			continue
		}
		replaced := false
		for _, other := range methods {
			if other.owner != method.owner && other.owner.supers[method.owner.declaration.Symbol] != nil {
				replaced = true
				break
			}
		}
		if !replaced {
			if selected != nil {
				return nil
			}
			selected = method
		}
	}
	return selected
}

func (method *javaInterfaceMethod) defaultName() string {
	return "GppJavaDefault_" + method.owner.declaration.Name + "_" + method.goName
}

func (iface *javaInterface) storageName(name string) string {
	return iface.symbolPrefix + name
}

func (program *javaObjectProgram) interfaceReference(iface *javaInterface, name string) string {
	if program.facadeOf != "" && iface.file.Package == program.pkg {
		return name
	}
	if iface.storagePackage == program.pkg || iface.storagePackage == program.facadeOf {
		return program.qualify(iface.storagePackage, iface.storageName(name))
	}
	return program.qualify(iface.file.Package, name)
}

func (program *javaObjectProgram) emitInterface(out *strings.Builder, iface *javaInterface) error {
	name := iface.storageName(iface.declaration.Name)
	fmt.Fprintf(out, "\ntype %s interface {\n", name)
	for _, parent := range iface.declaration.Supertypes {
		fmt.Fprintf(out, "\t%s\n", program.objectGoType(parent))
	}
	if len(iface.declaration.Supertypes) != 0 {
		fmt.Fprintf(out, "\tIs%s()\n", iface.declaration.Name)
	}
	for _, method := range iface.methods {
		parameters, _ := program.objectParameterList(method.declaration.Parameters)
		fmt.Fprintf(out, "\t%s(%s)%s\n", method.goName, parameters, program.interfaceResult(method.declaration))
	}
	out.WriteString("}\n")
	for _, field := range iface.constants {
		if err := program.emitInterfaceConstant(out, iface, field, ""); err != nil {
			return unsupportedSource(iface.file, field, err.Error())
		}
	}
	for _, method := range iface.methods {
		if !method.isDefault {
			continue
		}
		if err := program.emitInterfaceDefault(out, method, ""); err != nil {
			return unsupportedSource(iface.file, method.declaration, err.Error())
		}
	}
	if iface.checksErrors {
		emitJavaCheckedResult(out, iface.checkedResultName())
	}
	return nil
}

func (program *javaObjectProgram) interfaceResult(declaration Occurrence) string {
	if declaration.Result == "void" {
		return ""
	}
	return " " + program.objectGoType(declaration.Result)
}

func (program *javaObjectProgram) emitInterfaceConstant(out *strings.Builder, iface *javaInterface, field Occurrence, facade string) error {
	name := iface.declaration.Name + field.Name
	target := ""
	if facade != "" {
		target = program.qualify(facade, iface.storageName(name))
	} else {
		name = iface.storageName(name)
	}
	if field.Type == javaStringType {
		call := target + "()"
		if facade == "" {
			var err error
			call, err = program.stringLiteral(field.ConstantUTF16LE)
			if err != nil {
				return err
			}
		}
		fmt.Fprintf(out, "\nfunc %s() %s { return %s }\n", name, program.objectGoType(field.Type), call)
		return nil
	}
	if facade == "" {
		value, err := primitiveConstantLiteral(field.Type, *field.Constant)
		if err != nil {
			return err
		}
		target = value
	}
	fmt.Fprintf(out, "\nconst %s = %s\n", name, target)
	return nil
}

func (program *javaObjectProgram) emitInterfaceDefault(out *strings.Builder, method *javaInterfaceMethod, facade string) error {
	declaration := method.declaration
	iface := method.owner
	name := method.defaultName()
	parameters, arguments := program.objectParameterList(declaration.Parameters)
	receiver := program.objectGoType(iface.declaration.Symbol)
	if parameters != "" {
		parameters, arguments = ", "+parameters, ", "+arguments
	}
	result := program.interfaceResult(declaration)
	if facade != "" {
		target := program.qualify(facade, iface.storageName(name))
		fmt.Fprintf(out, "\nfunc %s(this %s%s)%s { %s%s(this%s) }\n",
			name, receiver, parameters, result, returnKeyword(result), target, arguments)
		return nil
	}
	resultType := declaration.Result
	hasTry := declaration.Body.containsTry()
	if hasTry && resultType != "void" {
		result = " (javaResult " + program.objectGoType(resultType) + ")"
	}
	fmt.Fprintf(out, "\nfunc %s(this %s%s)%s {\n", iface.storageName(name), receiver, parameters, result)
	ctx := javaObjectContext{program: program, iface: iface, controlID: new(int),
		arguments: objectParameterNames(declaration.Parameters)}
	if iface.hasAssertions {
		program.runtime = true
		fmt.Fprintf(out, "\t%s\n", iface.assertionStatus().enabled())
	}
	if hasTry {
		ctx.emitJavaReturnBoundary(out, resultType)
	}
	if err := ctx.emitPatternStatements(out, declaration.Body, resultType); err != nil {
		return err
	}
	if resultType != "void" && (hasTry || declaration.Body.containsStatement(needsJavaControlFlowTerminal)) {
		out.WriteString("\tpanic(\"Java method completed without a result\")\n")
	}
	out.WriteString("}\n")
	return nil
}

func (iface *javaInterface) checkedResultName() string {
	return "javaCheckedResult" + iface.storageName(iface.declaration.Name)
}

func (ctx javaObjectContext) interfaceReferenceCall(method string, iface *javaInterface, value string) string {
	ctx.program.runtime = true
	return "javart." + method + "[" + ctx.program.objectGoType(iface.declaration.Symbol) + "](" + value +
		", " + strconv.Quote(iface.declaration.Symbol) + ")"
}

func (ctx javaObjectContext) defaultCall(method *javaInterfaceMethod, receiver, arguments string) string {
	if arguments != "" {
		arguments = ", " + arguments
	}
	return ctx.program.interfaceReference(method.owner, method.defaultName()) + "(" +
		ctx.interfaceReferenceCall("CastInterface", method.owner, receiver) + arguments + ")"
}

func (ctx javaObjectContext) invokeInterface(expression *JavaExpression, method *javaInterfaceMethod) (string, error) {
	receiver := "this"
	if expression.Target != nil {
		var err error
		receiver, err = ctx.expression(expression.Target)
		if err != nil {
			return "", err
		}
	} else if expression.Receiver != "this" && expression.Receiver != "super" && expression.Receiver != "interface-super" {
		return "", fmt.Errorf("unsupported Java interface invocation %s", expression.Symbol)
	}
	call := func(receiver, arguments string) string {
		receiver = ctx.interfaceReferenceCall("CastInterface", method.owner, receiver)
		return receiver + "." + method.goName + "(" + arguments + ")"
	}
	if expression.Receiver == "super" || expression.Receiver == "interface-super" {
		if !method.isDefault {
			return "", fmt.Errorf("cannot invoke an abstract Java interface method")
		}
		call = func(receiver, arguments string) string {
			return ctx.defaultCall(method, receiver, arguments)
		}
	}
	return ctx.emitReferenceInvocation(expression, method.declaration, receiver, call)
}

func returnKeyword(result string) string {
	if result == "" {
		return ""
	}
	return "return "
}

func (class *javaObjectClass) emitInterfaceMarkers(out *strings.Builder, name string) {
	inherited := map[string]bool{}
	if class.parent != nil {
		inherited = interfaceMarkers(class.parent.interfaces)
	}
	for _, marker := range slices.Sorted(maps.Keys(interfaceMarkers(class.interfaces))) {
		if !inherited[marker] {
			fmt.Fprintf(out, "\nfunc (*%s) %s() {}\n", name, marker)
		}
	}
	fmt.Fprintf(out, "\nfunc (*%s) %s(javaInterface string) bool {\n", name, javaImplementsBridge)
	if len(class.interfaces) != 0 {
		interfaces := slices.Sorted(maps.Keys(class.interfaces))
		for index, key := range interfaces {
			interfaces[index] = strconv.Quote(key)
		}
		fmt.Fprintf(out, "\tswitch javaInterface {\n\tcase %s:\n\t\treturn true\n\t}\n", strings.Join(interfaces, ", "))
	}
	out.WriteString("\treturn false\n}\n")
}
