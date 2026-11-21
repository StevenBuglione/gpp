package javaport

import (
	"fmt"
	"go/token"
	"maps"
	"slices"
	"strings"
)

func javaStaticObservable(declaration Occurrence) bool {
	return slices.Contains(declaration.Modifiers, "PUBLIC") || slices.Contains(declaration.Modifiers, "PROTECTED")
}

func (program *javaObjectProgram) reserveStaticName(class *javaObjectClass, name string) error {
	exports := program.exports[class.file.Package]
	if exports[name] {
		return fmt.Errorf("duplicate exported Go static member %s", name)
	}
	exports[name] = true
	return nil
}

func (program *javaObjectProgram) addStaticMethod(class *javaObjectClass, declaration Occurrence) error {
	if declaration.Name == "<init>" || declaration.HasTypeParams || declaration.Body == nil ||
		!hasOnlyModifiers(declaration, "STATIC", "FINAL", "PUBLIC", "PROTECTED", "PRIVATE") {
		return fmt.Errorf("unsupported Java static method modifiers, body, or type parameters")
	}
	for _, parameter := range declaration.Parameters {
		if program.mappedObjectType(parameter.Type) == "" {
			return fmt.Errorf("unsupported Java static method parameter type %s", parameter.Type)
		}
	}
	if declaration.Result != "void" && program.mappedObjectType(declaration.Result) == "" {
		return fmt.Errorf("unsupported Java static method result %s", declaration.Result)
	}
	name := exportMethodName(declaration.Name)
	if !token.IsExported(name) || !token.IsIdentifier(name) {
		return fmt.Errorf("unsupported Java static method name")
	}
	method := &javaObjectMethod{class: class, declaration: declaration, goName: name,
		static: true, public: slices.Contains(declaration.Modifiers, "PUBLIC")}
	for _, previous := range class.staticMethods {
		if previous.goName == name {
			return fmt.Errorf("overloaded or colliding Java static method %s", declaration.Name)
		}
	}
	if javaStaticObservable(declaration) {
		if err := program.reserveStaticName(class, method.staticName()); err != nil {
			return err
		}
	}
	if method.hasErrorAdapter() {
		if err := program.reserveStaticName(class, method.staticAdapterName()); err != nil {
			return err
		}
	}
	class.staticMethods = append(class.staticMethods, method)
	program.methods[declaration.Symbol] = method
	return nil
}

func (method *javaObjectMethod) staticName() string {
	name := method.class.declaration.Name + method.goName
	switch {
	case method.public:
		return name
	case javaStaticObservable(method.declaration):
		return "GppJavaStatic_" + method.class.declaration.Name + "_" + method.goName
	default:
		return "javaStatic_" + method.class.declaration.Name + "_" + method.goName
	}
}

func (method *javaObjectMethod) staticAdapterName() string {
	return "GppCall" + method.staticName()
}

func (class *javaObjectClass) staticGuardName() string {
	return "GppJavaInitialize_" + class.declaration.Name
}

func (field javaObjectField) staticStorage() string {
	return field.class.storageName("javaStaticField_" + field.class.declaration.Name + "_" + field.declaration.Name)
}

func (field javaObjectField) staticAccessor(operation string) string {
	prefix := "java"
	if javaStaticObservable(field.declaration) && !(operation == "Set" && slices.Contains(field.declaration.Modifiers, "FINAL")) {
		prefix = "Gpp"
	}
	return prefix + operation + "Static_" + field.class.declaration.Name + "_" + field.declaration.Name
}

func (program *javaObjectProgram) addStaticField(class *javaObjectClass, declaration Occurrence) error {
	if !hasOnlyModifiers(declaration, "STATIC", "FINAL", "PUBLIC", "PROTECTED", "PRIVATE") {
		return fmt.Errorf("unsupported Java static field modifiers")
	}
	if declaration.Constant != nil {
		if slices.Contains(declaration.Modifiers, "PUBLIC") {
			return program.reserveStaticName(class, class.declaration.Name+declaration.Name)
		}
		return nil
	}
	if declaration.Initializer != nil {
		class.staticInitializers = append(class.staticInitializers, declaration)
	}
	if !javaStaticObservable(declaration) {
		return nil
	}
	field := javaObjectField{declaration, class}
	if err := program.reserveStaticName(class, field.staticAccessor("Get")); err != nil {
		return err
	}
	if !slices.Contains(declaration.Modifiers, "FINAL") {
		return program.reserveStaticName(class, field.staticAccessor("Set"))
	}
	return nil
}

func (program *javaObjectProgram) layoutStaticInitialization(class *javaObjectClass) error {
	class.activeInitialization = len(class.staticInitializers) != 0 || class.hasAssertions ||
		(class.parent != nil && class.parent.activeInitialization)
	for _, iface := range class.interfaces {
		class.activeInitialization = class.activeInitialization || iface.hasAssertions
	}
	if class.activeInitialization {
		return program.reserveStaticName(class, class.staticGuardName())
	}
	return nil
}

func (program *javaObjectProgram) classInitialization(class *javaObjectClass) string {
	if !class.activeInitialization {
		return ""
	}
	return program.classReference(class, class.staticGuardName()) + "()"
}

func (program *javaObjectProgram) emitStaticClass(out *strings.Builder, class *javaObjectClass) error {
	if class.activeInitialization {
		if err := program.emitStaticGuard(out, class); err != nil {
			return err
		}
	}
	for _, declaration := range class.fields {
		if slices.Contains(declaration.Modifiers, "STATIC") && declaration.Constant == nil {
			program.emitStaticField(out, javaObjectField{declaration, class}, "")
		}
	}
	for _, method := range class.staticMethods {
		if err := program.emitStaticMethod(out, method, ""); err != nil {
			return unsupportedSource(class.file, method.declaration, err.Error())
		}
	}
	return nil
}

func (program *javaObjectProgram) emitStaticGuard(out *strings.Builder, class *javaObjectClass) error {
	program.runtime = true
	state := class.storageName("javaInitState_" + class.declaration.Name)
	fmt.Fprintf(out, "\nvar %s javart.ClassInitialization\n", state)
	fmt.Fprintf(out, "\nfunc %s() {\n\t%s.Ensure(%q, func() {\n", class.storageName(class.staticGuardName()), state, class.declaration.Symbol)
	// Capture this class's assertion selection before any recursive active use.
	ctx := (javaObjectContext{program: program, class: class, static: true, controlID: new(int)}).patternScope()
	ctx.emitAssertionInitialization(out)
	if class.parent != nil {
		if parent := program.classInitialization(class.parent); parent != "" {
			fmt.Fprintf(out, "\t%s\n", parent)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(class.interfaces)) {
		if iface := class.interfaces[name]; iface.hasAssertions {
			fmt.Fprintf(out, "\t%s\n", iface.assertionStatus().enabled())
		}
	}
	var body strings.Builder
	for _, initializer := range class.staticInitializers {
		if initializer.Kind == "BLOCK" {
			body.WriteString("\t{\n")
			if err := ctx.emitStatements(&body, initializer.Body, "void"); err != nil {
				return unsupportedSource(class.file, initializer, err.Error())
			}
			body.WriteString("\t}\n")
			continue
		}
		value, err := ctx.converted(initializer.Initializer, initializer.Type)
		if err != nil {
			return unsupportedSource(class.file, initializer, err.Error())
		}
		fmt.Fprintf(&body, "\t%s = %s\n", (javaObjectField{initializer, class}).staticStorage(), value)
	}
	ctx.writePatternBody(out, &body)
	out.WriteString("\t}, javart.UnwrapRaised, javart.Raise)\n}\n")
	return nil
}

func (program *javaObjectProgram) emitStaticField(out *strings.Builder, field javaObjectField, facade string) {
	goType := program.objectGoType(field.declaration.Type)
	get, set := field.staticAccessor("Get"), field.staticAccessor("Set")
	isFinal := slices.Contains(field.declaration.Modifiers, "FINAL")
	if facade != "" {
		fmt.Fprintf(out, "\nfunc %s() %s { return %s() }\n", get, goType,
			program.qualify(facade, field.class.storageName(get)))
		if !isFinal {
			fmt.Fprintf(out, "\nfunc %s(value %s) { %s(value) }\n", set, goType,
				program.qualify(facade, field.class.storageName(set)))
		}
		return
	}
	storage := field.staticStorage()
	guard := program.classInitialization(field.class)
	if guard != "" {
		guard += "; "
	}
	fmt.Fprintf(out, "\nvar %s %s\n", storage, goType)
	fmt.Fprintf(out, "\nfunc %s() %s { %sreturn %s }\n", field.class.storageName(get), goType, guard, storage)
	fmt.Fprintf(out, "\nfunc %s(value %s) { %s%s = value }\n", field.class.storageName(set), goType, guard, storage)
}

func (program *javaObjectProgram) emitStaticMethod(out *strings.Builder, method *javaObjectMethod, facade string) error {
	declaration, class := method.declaration, method.class
	name := method.staticName()
	parameters, arguments := program.objectParameterList(declaration.Parameters)
	result := program.objectResultType(method)
	ctx := javaObjectContext{program: program, class: class, static: true, controlID: new(int),
		arguments: objectParameterNames(declaration.Parameters)}
	if facade != "" {
		target := program.qualify(facade, class.storageName(name))
		fmt.Fprintf(out, "\nfunc %s(%s)%s { %s%s(%s) }\n", name, parameters, result, returnKeyword(result), target, arguments)
	} else {
		name = class.storageName(name)
		bodyResult := result
		hasTry := declaration.Body.containsTry()
		if hasTry && declaration.Result != "void" {
			bodyResult = " (javaResult " + program.objectGoType(declaration.Result) + ")"
		}
		fmt.Fprintf(out, "\nfunc %s(%s)%s {\n", name, parameters, bodyResult)
		if guard := program.classInitialization(class); guard != "" {
			fmt.Fprintf(out, "\t%s\n", guard)
		}
		if hasTry {
			ctx.emitJavaReturnBoundary(out, declaration.Result)
		}
		if err := ctx.emitPatternStatements(out, declaration.Body, declaration.Result); err != nil {
			return err
		}
		if declaration.Result != "void" && (hasTry || declaration.Body.containsStatement(isJavaLoop)) {
			out.WriteString("\tpanic(\"Java method completed without a result\")\n")
		}
		out.WriteString("}\n")
	}
	if method.hasErrorAdapter() {
		adapter := method.staticAdapterName()
		if facade == "" {
			adapter = class.storageName(adapter)
		}
		ctx.emitErrorAdapter(out, "", adapter, parameters, strings.TrimSpace(result), name+"("+arguments+")")
	}
	return nil
}

func (ctx javaObjectContext) staticQualifier(expression *JavaExpression) (string, error) {
	if expression.Receiver != "other" {
		return "", nil
	}
	if expression.Target == nil {
		return "", fmt.Errorf("missing attributed Java static qualifier for %s", expression.Symbol)
	}
	value, err := ctx.expression(expression.Target)
	if err != nil {
		return "", err
	}
	return "\t_ = " + value + "\n", nil
}

func (ctx javaObjectContext) staticFieldValue(expression *JavaExpression) (string, error) {
	if expression.UTF16LE != nil {
		return ctx.program.stringLiteral(expression.UTF16LE)
	}
	if expression.Literal != "" {
		return ctx.program.primitiveLowering.literal(expression.Type, expression.Literal)
	}
	copy := *expression
	copy.Receiver = "type"
	if value := ctx.program.primitiveLowering.booleanConstant(&copy); value != "" {
		return value, nil
	}
	if field, found := ctx.program.fields[expression.Symbol]; found {
		return ctx.program.classReference(field.class, field.staticAccessor("Get")) + "()", nil
	}
	return "", fmt.Errorf("unsupported Java static field access %s", expression.Symbol)
}

func (ctx javaObjectContext) readStaticField(expression *JavaExpression) (string, error) {
	qualifier, err := ctx.staticQualifier(expression)
	if err != nil {
		return "", err
	}
	value, err := ctx.staticFieldValue(expression)
	if err != nil || qualifier == "" {
		return value, err
	}
	return fmt.Sprintf("func() %s {\n%s\treturn %s\n}()", ctx.program.objectGoType(expression.Type), qualifier, value), nil
}

func staticMutationTarget(expression *JavaExpression) *JavaExpression {
	expression = javaMutationOperand(expression)
	if expression != nil && expression.Kind == "FIELD" && expression.Static {
		return expression
	}
	return nil
}

func (ctx javaObjectContext) staticFieldMutation(target *JavaExpression) (setup, load, setter string, err error) {
	field, found := ctx.program.fields[target.Symbol]
	if !found || field.declaration.Constant != nil {
		return "", "", "", fmt.Errorf("unsupported Java static field write %s", target.Symbol)
	}
	setup, err = ctx.staticQualifier(target)
	return setup, ctx.program.classReference(field.class, field.staticAccessor("Get")) + "()",
		ctx.program.classReference(field.class, field.staticAccessor("Set")), err
}

func (ctx javaObjectContext) assignStaticField(target, operand *JavaExpression) (string, error) {
	if operand == nil {
		return "", fmt.Errorf("missing attributed Java static assignment value")
	}
	setup, _, setter, err := ctx.staticFieldMutation(target)
	if err != nil {
		return "", err
	}
	value, err := ctx.converted(operand, target.Type)
	if err != nil {
		return "", err
	}
	goType := ctx.program.objectGoType(target.Type)
	return fmt.Sprintf("func() %s {\n%s\tvar javaValue %s = %s\n\t%s(javaValue)\n\treturn javaValue\n}()",
		goType, setup, goType, value, setter), nil
}

func (ctx javaObjectContext) callStatic(expression *JavaExpression, method *javaObjectMethod) (string, error) {
	qualifier, err := ctx.staticQualifier(expression)
	if err != nil {
		return "", err
	}
	return ctx.orderedStaticInvocation(expression, method, ctx.program.objectResultType(method), qualifier,
		ctx.program.classReference(method.class, method.staticName()))
}

func (ctx javaObjectContext) orderedStaticInvocation(expression *JavaExpression, method *javaObjectMethod, result, beforeArgs, name string) (string, error) {
	parameters := method.declaration.Parameters
	if len(parameters) != len(expression.Arguments) {
		return "", fmt.Errorf("unsupported Java invocation arity for %s", expression.Symbol)
	}
	var code strings.Builder
	fmt.Fprintf(&code, "func()%s {\n%s", result, beforeArgs)
	arguments := make([]string, 0, len(parameters))
	for index, parameter := range parameters {
		value, err := ctx.converted(expression.Arguments[index], parameter.Type)
		if err != nil {
			return "", err
		}
		argument := fmt.Sprintf("javaArgument%d", index)
		fmt.Fprintf(&code, "\tvar %s %s = %s\n", argument, ctx.program.objectGoType(parameter.Type), value)
		arguments = append(arguments, argument)
	}
	fmt.Fprintf(&code, "\t%s%s(%s)\n}()", returnKeyword(result), name, strings.Join(arguments, ", "))
	return code.String(), nil
}
