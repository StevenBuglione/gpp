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
	emitJavaPermits(out, class.declaration, class.storageName(javaPermitsAPI(class.declaration.Name)))
	if class.hasAssertions {
		program.runtime = true
		fmt.Fprintf(out, "\nvar %s javart.AssertionStatus\n", class.assertionStatus().storage)
	}
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
		out.WriteString("\tjavaObjectIdentity javart.ObjectIdentity\n")
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
	class.emitInterfaceMarkers(out, name)
	if err := program.emitInstanceFieldBridges(out, class, name); err != nil {
		return err
	}
	if err := program.emitStaticClass(out, class); err != nil {
		return err
	}
	if class.declaration.Kind == "RECORD" {
		fmt.Fprintf(out, "\nfunc (*%s) GppJavaRecord() {}\n", name)
	}

	ctx := javaObjectContext{program: program, class: class, controlID: new(int)}
	if len(class.initializers) > 0 {
		fmt.Fprintf(out, "\nfunc (this *%s) javaInitFields() {\n", name)
		var body strings.Builder
		initializerContext := ctx.patternScope()
		for _, initializer := range class.initializers {
			if initializer.Kind == "BLOCK" {
				body.WriteString("\t{\n")
				if err := initializerContext.emitStatements(&body, initializer.Body, "void"); err != nil {
					return unsupportedSource(class.file, initializer, err.Error())
				}
				body.WriteString("\t}\n")
			} else {
				value, err := initializerContext.converted(initializer.Initializer, initializer.Type)
				if err != nil {
					return unsupportedSource(class.file, initializer, err.Error())
				}
				fmt.Fprintf(&body, "\tthis.javaField%s = %s\n", initializer.Name, value)
			}
		}
		initializerContext.writePatternBody(out, &body)
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
		emitJavaCheckedResult(out, class.checkedResultName())
	}
	return nil
}

func emitJavaCheckedResult(out *strings.Builder, name string) {
	fmt.Fprintf(out, "\nfunc %s[T any](value T, failure error) T {\n\tif failure != nil {\n\t\tthrow failure\n\t}\n\treturn value\n}\n", name)
}

func (class *javaObjectClass) checkedResultName() string {
	return "javaCheckedResult" + class.storageName(class.declaration.Name)
}

type javaObjectContext struct {
	program        *javaObjectProgram
	class          *javaObjectClass
	iface          *javaInterface
	static         bool
	arguments      map[string]string
	caught         map[string]string
	locals         map[string]string
	tryScope       *javaTryBranches
	control        *javaControlTarget
	controlID      *int
	yield          *javaYieldTarget
	patterns       map[int64]string
	lambdaCaptures *javaLambdaCaptures
}

func (ctx javaObjectContext) emitConstructor(out *strings.Builder, method *javaObjectMethod) error {
	name := ctx.class.storageName(ctx.class.declaration.Name)
	declaration := method.declaration
	parameters, arguments := ctx.program.objectParameterList(declaration.Parameters)
	ctx.arguments = objectParameterNames(declaration.Parameters)
	if !ctx.class.isAbstract() {
		allocator := ctx.class.storageName(method.allocatorName())
		fmt.Fprintf(out, "\nfunc %s(%s) *%s {\n", allocator, parameters, name)
		if guard := ctx.program.classInitialization(ctx.class); guard != "" {
			fmt.Fprintf(out, "\t%s\n", guard)
		}
		fmt.Fprintf(out, "\tthis := new(%s)\n", name)
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
	ctx = ctx.patternScope()
	var body strings.Builder
	objectConstructor := call.Symbol == "java.lang.Object#<init>()" ||
		(ctx.class.declaration.Kind == "RECORD" && call.Symbol == javaRecordType+"#<init>()")
	if call.Receiver != "super" || !objectConstructor || ctx.class.parent != nil {
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
		fmt.Fprintf(&body, "\t%s.%s(%s)\n", receiver, calleeName, values)
	}
	if call.Receiver == "super" && len(ctx.class.initializers) > 0 {
		body.WriteString("\tthis.javaInitFields()\n")
	}
	if err := ctx.emitStatements(&body, declaration.Body, "void"); err != nil {
		return err
	}
	ctx.writePatternBody(out, &body)
	if ctx.class.declaration.Kind == "RECORD" && (declaration.Synthetic || declaration.CompactRecordConstructor) {
		for _, component := range ctx.class.declaration.RecordComponents {
			fmt.Fprintf(out, "\tthis.javaField%s = %s\n", component.Name, ctx.arguments[component.Name])
		}
	}
	out.WriteString("}\n")
	return nil
}

func (ctx javaObjectContext) emitMethod(out *strings.Builder, method *javaObjectMethod) error {
	if method.recordImplicit {
		return ctx.emitRecordMethod(out, method)
	}
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
	hasTry := method.defaultMethod == nil && declaration.Body.containsTry()
	if hasTry && declaration.Result != "void" {
		bodyResult = " (javaResult " + ctx.program.objectGoType(declaration.Result) + ")"
	}
	fmt.Fprintf(out, "\nfunc (this *%s) %s(%s)%s {\n", name, bodyName, parameters, bodyResult)
	ctx.emitAssertionInitialization(out)
	if hasTry {
		ctx.emitJavaReturnBoundary(out, declaration.Result)
	}
	if method.defaultMethod != nil {
		fmt.Fprintf(out, "\t%s%s\n", returnKeyword(result), ctx.defaultCall(method.defaultMethod, "this", arguments))
	} else if declaration.Body == nil {
		fmt.Fprintf(out, "\tpanic(%q)\n", "abstract Java method "+declaration.Symbol)
	} else {
		if err := ctx.emitPatternStatements(out, declaration.Body, declaration.Result); err != nil {
			return err
		}
		if declaration.Result != "void" && (hasTry || declaration.Body.containsStatement(needsJavaControlFlowTerminal)) {
			out.WriteString("\tpanic(\"Java method completed without a result\")\n")
		}
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
	return ctx.localScope().emitStatementsInScope(out, body, result)
}

func (ctx javaObjectContext) emitStatementsInScope(out *strings.Builder, body *MethodBody, result string) error {
	if body == nil {
		return fmt.Errorf("missing attributed statement sequence")
	}
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
			if ctx.tryScope != nil {
				return "javart.Return(struct{}{})", nil
			}
			return "return", nil
		}
		if expression != nil {
			value, err := ctx.converted(expression, result)
			if ctx.tryScope != nil && err == nil {
				return "javart.Return[" + ctx.program.objectGoType(result) + "](" + value + ")", nil
			}
			return "return " + value, err
		}
	case "THROW":
		return ctx.throwStatement(expression)
	case "ASSERT":
		return ctx.assertStatement(statement)
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
	case "SWITCH":
		return ctx.switchStatement(statement, result, "")
	case "YIELD":
		return ctx.switchYield(expression)
	case "WHILE_LOOP", "DO_WHILE_LOOP", "FOR_LOOP", "ENHANCED_FOR_LOOP":
		return ctx.loopStatement(statement, result, "")
	case "LABELED_STATEMENT":
		return ctx.labeledStatement(statement, result)
	case "BREAK", "CONTINUE":
		return ctx.loopBranch(statement)
	case "EMPTY_STATEMENT":
		return "", nil
	case "EXPRESSION_STATEMENT":
		if expression == nil {
			break
		}
		if expression.Kind == "METHOD_INVOCATION" {
			return ctx.invoke(expression)
		}
		switch expression.Kind {
		case "ASSIGNMENT":
			return ctx.assignment(expression, false)
		case "PREFIX_INCREMENT", "PREFIX_DECREMENT", "POSTFIX_INCREMENT", "POSTFIX_DECREMENT":
			return ctx.increment(expression)
		}
		if operator, compound := javaCompoundOperators[expression.Kind]; compound {
			return ctx.updateVariable(expression.Left, expression.Right, operator, false)
		}
	}
	return "", fmt.Errorf("unsupported statement %s", statement.Kind)
}

func (ctx javaObjectContext) converted(expression *JavaExpression, target string) (string, error) {
	if conditional := nullConditional(expression); conditional != nil {
		return emitJavaConditional(conditional, target, ctx.program.objectGoType, ctx.converted)
	}
	value, err := ctx.expression(expression)
	if err != nil {
		return "", err
	}
	return ctx.convertValue(value, expression.Type, target)
}

func (ctx javaObjectContext) convertValue(value, source, target string) (string, error) {
	if _, primitive := primitiveTypes[target]; !primitive {
		return ctx.convertReference(value, source, target)
	}
	return ctx.program.primitiveLowering.convertPrimitiveContext(value, source, target)
}

func (ctx javaObjectContext) expression(expression *JavaExpression) (string, error) {
	switch expression.Kind {
	case "THIS":
		return "this", nil
	case "IDENTIFIER":
		if name, found := ctx.arguments[expression.Name]; found {
			return ctx.lambdaCaptures.resolve(name), nil
		}
		return "", fmt.Errorf("unsupported expression IDENTIFIER at %d:%d", expression.Line, expression.Column)
	case "LOCAL_VARIABLE":
		if name, found := ctx.locals[expression.Name]; found {
			return ctx.lambdaCaptures.resolve(name), nil
		}
		return "", fmt.Errorf("cannot use Java local %s", expression.Name)
	case "BINDING_VARIABLE":
		if name := javaPatternName(expression.BindingID); ctx.lambdaCaptures.has(name) {
			return ctx.lambdaCaptures.resolve(name), nil
		}
		return ctx.patternSlot(expression.BindingID, expression.Type)
	case "CATCH_PARAMETER":
		if name, found := ctx.caught[expression.Name]; found {
			return ctx.lambdaCaptures.resolve(name), nil
		}
		return "", fmt.Errorf("cannot use caught parameter %s", strings.TrimSpace(expression.Name))
	case "FIELD":
		return ctx.field(expression)
	case "METHOD_INVOCATION":
		return ctx.invoke(expression)
	case "LAMBDA_EXPRESSION":
		return ctx.lambda(expression)
	case "MEMBER_REFERENCE":
		return ctx.methodReference(expression)
	case "NEW_CLASS":
		if expression.Symbol == "java.lang.Object#<init>()" {
			ctx.program.runtime = true
			return "javart.NewObject()", nil
		}
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
	case "PLUS":
		if expression.Type == javaStringType {
			return ctx.concatString(expression)
		}
		return ctx.program.primitiveLowering.binary(expression, ctx.expression)
	case "NULL_LITERAL":
		return "nil", nil
	case "EQUAL_TO", "NOT_EQUAL_TO":
		return ctx.referenceComparison(expression)
	case "CONDITIONAL_EXPRESSION":
		return emitJavaConditional(expression, conditionalResultType(expression), ctx.program.objectGoType, ctx.converted)
	case "NEW_ARRAY", "ARRAY_INITIALIZER":
		return ctx.newArray(expression)
	case "ARRAY_ACCESS":
		return ctx.arrayAccess(expression)
	case "ARRAY_LENGTH":
		if expression.Target == nil || ctx.arrayElementType(expression.Target.Type) == "" {
			return "", fmt.Errorf("unsupported Java array length expression")
		}
		array, err := ctx.expression(expression.Target)
		return ctx.arrayLength(array), err
	case "PARENTHESIZED":
		value, err := ctx.expression(expression.Operand)
		return "(" + value + ")", err
	case "TYPE_CAST":
		return ctx.cast(expression)
	case "INSTANCE_OF":
		return ctx.instanceOf(expression)
	case "INSTANCE_OF_BINDING":
		return ctx.instanceOfBinding(expression)
	case "INSTANCE_OF_RECORD_PATTERN":
		return ctx.instanceOfRecordPattern(expression)
	case "SWITCH_EXPRESSION":
		return ctx.switchExpression(expression)
	case "ASSIGNMENT":
		return ctx.assignment(expression, true)
	case "PREFIX_INCREMENT", "PREFIX_DECREMENT", "POSTFIX_INCREMENT", "POSTFIX_DECREMENT":
		return ctx.increment(expression)
	case "UNARY_PLUS", "UNARY_MINUS", "BITWISE_COMPLEMENT", "LOGICAL_COMPLEMENT":
		return ctx.program.primitiveLowering.unary(expression, ctx.expression)
	case "LEFT_SHIFT", "RIGHT_SHIFT", "UNSIGNED_RIGHT_SHIFT":
		return ctx.program.primitiveLowering.shift(expression, ctx.expression)
	case "BOOLEAN_LITERAL", "CHAR_LITERAL", "INT_LITERAL", "LONG_LITERAL", "FLOAT_LITERAL", "DOUBLE_LITERAL":
		return ctx.program.primitiveLowering.literal(expression.Type, expression.Literal)
	default:
		if operator, compound := javaCompoundOperators[expression.Kind]; compound {
			return ctx.updateVariable(expression.Left, expression.Right, operator, false)
		}
		if _, binary := javaPrimitiveOperators[expression.Kind]; binary {
			return ctx.program.primitiveLowering.binary(expression, ctx.expression)
		}
		return ctx.program.primitiveLowering.expression(expression, ctx.arguments)
	}
}

func (ctx javaObjectContext) field(expression *JavaExpression) (string, error) {
	if expression.Static {
		return ctx.readStaticField(expression)
	}
	field, err := ctx.instanceField(expression)
	if err != nil {
		return "", err
	}
	return field.read(field.checkedReceiver(field.receiver)), nil
}

func (ctx javaObjectContext) invoke(expression *JavaExpression) (string, error) {
	switch expression.Symbol {
	case "java.lang.Object#toString()", "java.lang.Object#hashCode()":
		return ctx.invokeObjectStringMethod(expression)
	}
	if method := ctx.program.methods[expression.Symbol]; method != nil && method.static {
		return ctx.callStatic(expression, method)
	}
	if method := ctx.program.interfaceMethods[expression.Symbol]; method != nil {
		return ctx.invokeInterface(expression, method)
	}
	receiverClass, _, _ := strings.Cut(expression.Symbol, "#")
	if receiverClass == javaRecordType && expression.Target != nil {
		return ctx.invokeRecordReference(expression)
	}
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
	if expression.Target != nil {
		switch expression.Symbol {
		case "java.lang.Throwable#getMessage()":
			if javaCatchTypes[expression.Target.Type] != "" {
				value, err := ctx.expression(expression.Target)
				ctx.program.runtime = true
				return "javart.ThrowableMessage(" + value + ")", err
			}
		case "java.lang.Throwable#getCause()":
			if expression.Target.Type == javaExceptionInInitializerError {
				value, err := ctx.expression(expression.Target)
				ctx.program.runtime = true
				return "javart.ClassInitializationCause(" + value + ")", err
			}
			if expression.Target.Type == javaAssertionError || expression.Target.Type == javaMatchException {
				value, err := ctx.expression(expression.Target)
				ctx.program.runtime = true
				return "javart.RequireNonNull(" + value + ").GetCause()", err
			}
		}
	}
	if expression.Symbol == "java.lang.Throwable#getSuppressed()" && expression.Target != nil {
		value, err := ctx.expression(expression.Target)
		ctx.program.runtime = true
		return "javart.SuppressedArray(" + value + ")", err
	}
	callee := ctx.program.methods[expression.Symbol]
	if callee != nil && expression.Target != nil && ctx.program.classes[expression.Target.Type] != nil && callee.public {
		return ctx.invokeReference(expression, callee)
	}
	if callee == nil || ctx.class == nil || callee.declaration.Name == "<init>" ||
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
