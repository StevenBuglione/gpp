package javaport

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

func (program *javaObjectProgram) emitClass(out *strings.Builder, class *javaObjectClass) error {
	name := class.storageName(class.declaration.Name)
	if class.parent != nil && class.parent.file.Package != class.file.Package {
		fmt.Fprintf(out, "\ntype %s = %s\n", class.baseField(),
			program.classReference(class.parent, class.parent.declaration.Name))
	}
	for _, field := range class.fields {
		if !javaPublicConstant(field) {
			continue
		}
		if field.Type == javaStringType {
			literal, err := program.stringLiteral(field.ConstantUTF16LE)
			if err != nil {
				return unsupportedSource(class.file, field, err.Error())
			}
			fmt.Fprintf(out, "\nfunc %s() *javart.String { return %s }\n", class.constantStorageName(field), literal)
			continue
		}
		literal, err := primitiveConstantLiteral(field.Type, *field.Constant)
		if err != nil {
			return unsupportedSource(class.file, field, err.Error())
		}
		fmt.Fprintf(out, "\nconst %s = %s\n", class.constantStorageName(field), literal)
	}
	fmt.Fprintf(out, "\ntype %s struct {\n", name)
	if class.parent != nil {
		fmt.Fprintf(out, "\t%s\n", class.baseField())
	} else {
		out.WriteString("\tjavaObjectOwner any\n")
	}
	for _, field := range class.fields {
		if !slices.Contains(field.Modifiers, "STATIC") {
			fmt.Fprintf(out, "\tjavaField%s %s\n", field.Name, program.objectGoType(field.Type))
		}
	}
	for _, method := range class.methods {
		if method.overridden {
			parameters, _ := program.objectParameterList(method.declaration.Parameters)
			fmt.Fprintf(out, "\tjavaDispatch%s func(%s)%s\n", method.goName, parameters, program.objectResultType(method))
		}
	}
	out.WriteString("}\n")
	if class.closeable && (class.parent == nil || !class.parent.closeable) {
		fmt.Fprintf(out, "\nfunc (*%s) GppAutoCloseable() {}\n", name)
	}
	if class.embed {
		fmt.Fprintf(out, "\ntype javaBase%s = %s\n", name, name)
	}
	for ancestor := class.parent; ancestor != nil; ancestor = ancestor.parent {
		view := "&this." + class.baseField()
		if ancestor != class.parent {
			view = "this." + class.baseField() + "." + objectViewName(ancestor) + "()"
		}
		fmt.Fprintf(out, "\nfunc (this *%s) %s() %s {\n\tif this == nil { return nil }\n\treturn %s\n}\n",
			name, objectViewName(ancestor), program.objectGoType(ancestor.declaration.Symbol), view)
	}
	program.emitObjectIdentity(out, class, name)

	ctx := javaObjectContext{program: program, class: class}
	if len(class.initializers) > 0 {
		fmt.Fprintf(out, "\nfunc (this *%s) javaInitFields() {\n", name)
		for _, initializer := range class.initializers {
			if initializer.Kind == "BLOCK" {
				if err := ctx.emitStatements(out, initializer.Body, "void"); err != nil {
					return unsupportedSource(class.file, initializer, err.Error())
				}
			} else {
				value, err := ctx.converted(initializer.Initializer, initializer.Type)
				if err != nil {
					return unsupportedSource(class.file, initializer, err.Error())
				}
				fmt.Fprintf(out, "\tthis.javaField%s = %s\n", initializer.Name, value)
			}
		}
		out.WriteString("}\n")
	}
	for _, constructor := range class.constructors {
		if err := ctx.emitConstructor(out, constructor); err != nil {
			return unsupportedSource(class.file, constructor.declaration, err.Error())
		}
	}
	for _, method := range class.methods {
		if err := ctx.emitMethod(out, method); err != nil {
			return unsupportedSource(class.file, method.declaration, err.Error())
		}
	}
	if class.checksErrors {
		fmt.Fprintf(out, "\nfunc %s[T any](value T, failure error) T {\n\tif failure != nil {\n\t\tthrow failure\n\t}\n\treturn value\n}\n", class.checkedResultName())
	}
	return nil
}

func (class *javaObjectClass) checkedResultName() string {
	return "javaCheckedResult" + class.storageName(class.declaration.Name)
}

type javaObjectContext struct {
	program   *javaObjectProgram
	class     *javaObjectClass
	arguments map[string]string
	caught    map[string]string
	locals    map[string]string
	inTry     bool
}

func (ctx javaObjectContext) emitConstructor(out *strings.Builder, method *javaObjectMethod) error {
	name := ctx.class.storageName(ctx.class.declaration.Name)
	declaration := method.declaration
	parameters, arguments := ctx.program.objectParameterList(declaration.Parameters)
	ctx.arguments = objectParameterNames(declaration.Parameters)
	allocator := ctx.class.storageName(method.allocatorName())
	fmt.Fprintf(out, "\nfunc %s(%s) *%s {\n\tthis := new(%s)\n", allocator, parameters, name, name)
	if ctx.class.parent != nil {
		out.WriteString("\tthis.GppJavaBindOwner(this)\n")
	}
	// Attach the most-derived implementations before entering any Java constructor.
	for _, methodName := range slices.Sorted(maps.Keys(ctx.class.virtual)) {
		implementation := ctx.class.virtual[methodName]
		slot := implementation.slot
		if implementation == slot {
			continue
		}
		receiver, _ := ctx.class.memberReceiver(slot.class)
		owner, _ := ctx.class.memberReceiver(implementation.class)
		bodyName := implementation.internal
		if implementation.class.file.Package != ctx.class.file.Package {
			bodyName = implementation.bridgeName("Body")
		}
		if slot.class.file.Package != ctx.class.file.Package {
			fmt.Fprintf(out, "\t%s.%s(%s.%s)\n", receiver, slot.bridgeName("Dispatch"), owner, bodyName)
		} else {
			fmt.Fprintf(out, "\t%s.javaDispatch%s = %s.%s\n", receiver, slot.goName, owner, bodyName)
		}
	}
	fmt.Fprintf(out, "\tthis.%s(%s)\n\treturn this\n}\n", method.internal, arguments)
	if method.hasErrorAdapter() {
		ctx.emitErrorAdapter(out, "", ctx.class.storageName(method.errorAdapterName()), parameters, "*"+name, allocator+"("+arguments+")")
	}
	if method.crossPackageInit {
		fmt.Fprintf(out, "\nfunc (this *%s) %s(%s) { this.%s(%s) }\n", name,
			method.bridgeName("Init"), parameters, method.internal, arguments)
	}
	call := declaration.ConstructorCall
	if call == nil || (call.Receiver != "this" && call.Receiver != "super") {
		return fmt.Errorf("requires a resolved this(...) or super(...) invocation before the constructor body")
	}
	fmt.Fprintf(out, "\nfunc (this *%s) %s(%s) {\n", name, method.internal, parameters)
	if declaration.Body.containsTry() {
		ctx.emitJavaReturnBoundary(out, "void")
	}
	if call.Receiver != "super" || call.Symbol != "java.lang.Object#<init>()" || ctx.class.parent != nil {
		callee := ctx.program.methods[call.Symbol]
		expected := ctx.class
		if call.Receiver == "super" {
			expected = ctx.class.parent
		}
		if callee == nil || callee.class != expected || callee.declaration.Name != "<init>" {
			return fmt.Errorf("unsupported Java constructor target %s", call.Symbol)
		}
		values, err := ctx.callArguments(call, callee)
		if err != nil {
			return err
		}
		receiver, _ := ctx.class.memberReceiver(callee.class)
		calleeName := callee.internal
		if callee.class.file.Package != ctx.class.file.Package {
			calleeName = callee.bridgeName("Init")
		}
		fmt.Fprintf(out, "\t%s.%s(%s)\n", receiver, calleeName, values)
	}
	if call.Receiver == "super" && len(ctx.class.initializers) > 0 {
		out.WriteString("\tthis.javaInitFields()\n")
	}
	if err := ctx.emitStatements(out, declaration.Body, "void"); err != nil {
		return err
	}
	out.WriteString("}\n")
	return nil
}

func (ctx javaObjectContext) emitMethod(out *strings.Builder, method *javaObjectMethod) error {
	name := ctx.class.storageName(ctx.class.declaration.Name)
	declaration := method.declaration
	parameters, arguments := ctx.program.objectParameterList(declaration.Parameters)
	result := ctx.program.objectResultType(method)
	ctx.arguments = objectParameterNames(declaration.Parameters)
	bodyName := method.internal
	if method.public && method.slot == method && !method.overridden {
		bodyName = method.goName
	}
	bodyResult := result
	hasTry := declaration.Body.containsTry()
	if hasTry && declaration.Result != "void" {
		bodyResult = " (javaResult " + ctx.program.objectGoType(declaration.Result) + ")"
	}
	fmt.Fprintf(out, "\nfunc (this *%s) %s(%s)%s {\n", name, bodyName, parameters, bodyResult)
	if hasTry {
		ctx.emitJavaReturnBoundary(out, declaration.Result)
	}
	if err := ctx.emitStatements(out, declaration.Body, declaration.Result); err != nil {
		return err
	}
	if hasTry && declaration.Result != "void" {
		out.WriteString("\tpanic(\"Java method completed without a result\")\n")
	}
	out.WriteString("}\n")
	if ctx.class.crossPackageDescendant && method.public && bodyName != method.goName {
		returnKeyword := ""
		if result != "" {
			returnKeyword = "return "
		}
		fmt.Fprintf(out, "\nfunc (this *%s) %s(%s)%s { %sthis.%s(%s) }\n",
			name, method.bridgeName("Body"), parameters, result, returnKeyword, bodyName, arguments)
	}
	if ctx.class.crossPackageDescendant && method.overridden {
		fmt.Fprintf(out, "\nfunc (this *%s) %s(dispatch func(%s)%s) { this.javaDispatch%s = dispatch }\n",
			name, method.bridgeName("Dispatch"), parameters, result, method.goName)
	}
	if method.overridden {
		fmt.Fprintf(out, "\nfunc (this *%s) %s(%s)%s {\n", name, method.goName, parameters, result)
		fmt.Fprintf(out, "\tif dispatch := this.javaDispatch%s; dispatch != nil {\n", method.goName)
		if result == "" {
			fmt.Fprintf(out, "\t\tdispatch(%s)\n\t\treturn\n\t}\n\tthis.%s(%s)\n}\n", arguments, bodyName, arguments)
		} else {
			fmt.Fprintf(out, "\t\treturn dispatch(%s)\n\t}\n\treturn this.%s(%s)\n}\n", arguments, bodyName, arguments)
		}
	}
	if method.hasErrorAdapter() {
		ctx.emitErrorAdapter(out, "(this *"+name+") ", method.errorAdapterName(), parameters,
			strings.TrimSpace(result), "this."+method.goName+"("+arguments+")")
	}
	return nil
}

func (ctx javaObjectContext) emitErrorAdapter(out *strings.Builder, receiver, name, parameters, result, call string) {
	ctx.program.runtime = true
	if result == "" {
		fmt.Fprintf(out, "\nfunc %s%s(%s) (failure error) {\n\tdefer javart.RecoverError(&failure)\n\t%s\n\treturn nil\n}\n",
			receiver, name, parameters, call)
		return
	}
	fmt.Fprintf(out, "\nfunc %s%s(%s) (value %s, failure error) {\n\tdefer javart.RecoverError(&failure)\n\treturn %s, nil\n}\n",
		receiver, name, parameters, result, call)
}

func (ctx javaObjectContext) emitStatements(out *strings.Builder, body *MethodBody, result string) error {
	if body == nil {
		return fmt.Errorf("missing attributed statement sequence")
	}
	ctx = ctx.localScope()
	for index, statement := range body.Code {
		if statement.Kind == "VARIABLE" && statement.Variable != nil {
			name := fmt.Sprintf("javaLocal%d_%d", len(ctx.locals), index)
			if err := ctx.emitVariable(out, *statement.Variable, name); err != nil {
				return fmt.Errorf("%d:%d: %w", statement.Line, statement.Column, err)
			}
			continue
		}
		code, err := ctx.statement(statement, result)
		if err != nil {
			return fmt.Errorf("%d:%d: %w", statement.Line, statement.Column, err)
		}
		fmt.Fprintf(out, "\t%s\n", code)
	}
	return nil
}

func (ctx javaObjectContext) statement(statement JavaStatement, result string) (string, error) {
	expression := statement.Expression
	switch statement.Kind {
	case "RETURN":
		if result == "void" && expression == nil {
			if ctx.inTry {
				return "javart.Return(struct{}{})", nil
			}
			return "return", nil
		}
		if expression != nil {
			value, err := ctx.converted(expression, result)
			if ctx.inTry && err == nil {
				return "javart.Return[" + ctx.program.objectGoType(result) + "](" + value + ")", nil
			}
			return "return " + value, err
		}
	case "THROW":
		return ctx.throwStatement(expression)
	case "TRY":
		return ctx.tryStatement(statement, result)
	case "BLOCK":
		var body strings.Builder
		if err := ctx.emitStatements(&body, statement.Body, result); err != nil {
			return "", err
		}
		return "{\n" + body.String() + "}", nil
	case "IF":
		return ctx.ifStatement(statement, result)
	case "EXPRESSION_STATEMENT":
		if expression == nil {
			break
		}
		if expression.Kind == "METHOD_INVOCATION" {
			return ctx.invoke(expression)
		}
		if expression.Kind == "ASSIGNMENT" && expression.Left != nil && !expression.Left.Static && expression.Right != nil &&
			(expression.Left.Kind == "FIELD" || expression.Left.Kind == "LOCAL_VARIABLE") {
			target, err := ctx.expression(expression.Left)
			if err != nil {
				return "", err
			}
			value, err := ctx.converted(expression.Right, expression.Left.Type)
			return target + " = " + value, err
		}
	}
	return "", fmt.Errorf("unsupported statement %s", statement.Kind)
}

func (ctx javaObjectContext) converted(expression *JavaExpression, target string) (string, error) {
	value, err := ctx.expression(expression)
	if err != nil {
		return "", err
	}
	if _, primitive := primitiveTypes[target]; !primitive {
		return ctx.convertReference(value, expression.Type, target)
	}
	return ctx.program.primitiveLowering.convertPrimitiveContext(value, expression.Type, target)
}

func (ctx javaObjectContext) expression(expression *JavaExpression) (string, error) {
	switch expression.Kind {
	case "THIS":
		return "this", nil
	case "LOCAL_VARIABLE":
		if name, found := ctx.locals[expression.Name]; found {
			return name, nil
		}
		return "", fmt.Errorf("cannot use Java local %s", expression.Name)
	case "CATCH_PARAMETER":
		if name, found := ctx.caught[expression.Name]; found {
			return name, nil
		}
		return "", fmt.Errorf("cannot use caught parameter %s", strings.TrimSpace(expression.Name))
	case "FIELD":
		return ctx.field(expression)
	case "METHOD_INVOCATION":
		return ctx.invoke(expression)
	case "NEW_CLASS":
		if ctx.program.classes[expression.Type] != nil {
			return ctx.allocateObject(expression)
		}
		if wrapperPrimitive(expression.Type) != "" {
			return ctx.program.primitiveLowering.wrapperCall(expression, ctx.expression)
		}
		if _, throwable := javaCatchTypes[expression.Type]; throwable {
			return ctx.newThrowable(expression)
		}
		return ctx.newString(expression)
	case "STRING_LITERAL":
		return ctx.program.stringLiteral(expression.UTF16LE)
	case "NULL_LITERAL":
		return "nil", nil
	case "EQUAL_TO", "NOT_EQUAL_TO":
		return ctx.referenceComparison(expression)
	case "ARRAY_LENGTH":
		if expression.Target == nil || expression.Target.Type != javaThrowableArray {
			return "", fmt.Errorf("unsupported Java array length expression")
		}
		array, err := ctx.expression(expression.Target)
		ctx.program.runtime = true
		return "int32(len(javart.RequireNonNull(" + array + ")))", err
	case "PARENTHESIZED":
		value, err := ctx.expression(expression.Operand)
		return "(" + value + ")", err
	case "TYPE_CAST":
		return ctx.cast(expression)
	case "INSTANCE_OF":
		return ctx.instanceOf(expression)
	case "UNARY_PLUS", "UNARY_MINUS", "BITWISE_COMPLEMENT", "LOGICAL_COMPLEMENT":
		return ctx.program.primitiveLowering.unary(expression, ctx.expression)
	case "LEFT_SHIFT", "RIGHT_SHIFT", "UNSIGNED_RIGHT_SHIFT":
		return ctx.program.primitiveLowering.shift(expression, ctx.expression)
	case "BOOLEAN_LITERAL", "CHAR_LITERAL", "INT_LITERAL", "LONG_LITERAL", "FLOAT_LITERAL", "DOUBLE_LITERAL":
		return ctx.program.primitiveLowering.literal(expression.Type, expression.Literal)
	default:
		if _, binary := javaPrimitiveOperators[expression.Kind]; binary {
			return ctx.program.primitiveLowering.binary(expression, ctx.expression)
		}
		return ctx.program.primitiveLowering.expression(expression, ctx.arguments)
	}
}

func (ctx javaObjectContext) field(expression *JavaExpression) (string, error) {
	if expression.Static && expression.UTF16LE != nil && expression.Receiver != "other" {
		return ctx.program.stringLiteral(expression.UTF16LE)
	}
	if expression.Static && expression.Literal != "" && expression.Receiver != "other" {
		return ctx.program.primitiveLowering.literal(expression.Type, expression.Literal)
	}
	if value := ctx.program.primitiveLowering.booleanConstant(expression); value != "" {
		return value, nil
	}
	field, found := ctx.program.fields[expression.Symbol]
	if !found || expression.Static || (expression.Receiver != "this" && expression.Receiver != "super") {
		return "", fmt.Errorf("unsupported field access %s", expression.Symbol)
	}
	if field.class.file.Package != ctx.class.file.Package {
		return "", fmt.Errorf("cross-package field access requires a Java access bridge for %s", expression.Symbol)
	}
	receiver, found := ctx.class.memberReceiver(field.class)
	if !found {
		return "", fmt.Errorf("field %s is outside the current class hierarchy", expression.Symbol)
	}
	return receiver + ".javaField" + field.declaration.Name, nil
}

func (ctx javaObjectContext) invoke(expression *JavaExpression) (string, error) {
	receiverClass, _, _ := strings.Cut(expression.Symbol, "#")
	if wrapperPrimitive(receiverClass) != "" || receiverClass == javaNumberType {
		return ctx.program.primitiveLowering.wrapperCall(expression, ctx.expression)
	}
	if expression.Target != nil && expression.Target.Type == javaStringType {
		return ctx.invokeString(expression)
	}
	if expression.Symbol == "java.lang.Throwable#getMessage()" && expression.Target != nil &&
		(expression.Target.Type == javaIOException || expression.Target.Type == javaArithmeticException) {
		value, err := ctx.expression(expression.Target)
		ctx.program.runtime = true
		return "javart.RequireNonNull(" + value + ").GetMessage()", err
	}
	if expression.Symbol == "java.lang.Throwable#getSuppressed()" && expression.Target != nil {
		value, err := ctx.expression(expression.Target)
		ctx.program.runtime = true
		return "javart.Suppressed(" + value + ")", err
	}
	callee := ctx.program.methods[expression.Symbol]
	if callee != nil && expression.Target != nil && ctx.program.classes[expression.Target.Type] != nil && callee.public {
		return ctx.invokeReference(expression, callee)
	}
	if callee == nil || callee.declaration.Name == "<init>" ||
		(expression.Receiver != "this" && expression.Receiver != "super") {
		return "", fmt.Errorf("unsupported Java method invocation %s", expression.Symbol)
	}
	values, err := ctx.callArguments(expression, callee)
	if err != nil {
		return "", err
	}
	owner, name := callee.class, callee.internal
	if callee.public {
		if expression.Receiver == "this" {
			owner, name = callee.slot.class, callee.goName
		} else if callee.slot == callee && !callee.overridden {
			name = callee.goName
		} else if callee.class.file.Package != ctx.class.file.Package {
			name = callee.bridgeName("Body")
		}
	}
	receiver, found := ctx.class.memberReceiver(owner)
	if !found {
		return "", fmt.Errorf("method %s is outside the current class hierarchy", expression.Symbol)
	}
	return fmt.Sprintf("%s.%s(%s)", receiver, name, values), nil
}

func (ctx javaObjectContext) callArguments(call *JavaExpression, callee *javaObjectMethod) (string, error) {
	parameters := callee.declaration.Parameters
	if len(call.Arguments) != len(parameters) {
		return "", fmt.Errorf("unsupported Java invocation arity for %s", call.Symbol)
	}
	values := make([]string, 0, len(parameters))
	for index, argument := range call.Arguments {
		value, err := ctx.converted(argument, parameters[index].Type)
		if err != nil {
			return "", err
		}
		values = append(values, value)
	}
	return strings.Join(values, ", "), nil
}

func objectParameterNames(parameters []Parameter) map[string]string {
	names := make(map[string]string, len(parameters))
	for index, parameter := range parameters {
		names[parameter.Name] = "parameter" + strconv.Itoa(index)
	}
	return names
}

func (program *javaObjectProgram) objectParameterList(parameters []Parameter) (string, string) {
	declarations := make([]string, 0, len(parameters))
	arguments := make([]string, 0, len(parameters))
	for index, parameter := range parameters {
		name := "parameter" + strconv.Itoa(index)
		declarations = append(declarations, name+" "+program.objectGoType(parameter.Type))
		arguments = append(arguments, name)
	}
	return strings.Join(declarations, ", "), strings.Join(arguments, ", ")
}

func (program *javaObjectProgram) objectResultType(method *javaObjectMethod) string {
	if method.declaration.Result == "void" {
		return ""
	}
	return " " + program.objectGoType(method.declaration.Result)
}
