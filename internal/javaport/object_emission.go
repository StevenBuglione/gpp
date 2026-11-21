package javaport

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
)

func (program *javaObjectProgram) emitClass(out *strings.Builder, class *javaObjectClass) error {
	name := class.declaration.Name
	for _, field := range class.fields {
		if !slices.Contains(field.Modifiers, "STATIC") || !slices.Contains(field.Modifiers, "PUBLIC") {
			continue
		}
		if field.Type == javaStringType {
			literal, err := program.stringLiteral(field.ConstantUTF16LE)
			if err != nil {
				return unsupportedSource(class.file, field, err.Error())
			}
			fmt.Fprintf(out, "\nfunc %s%s() *javart.String { return %s }\n", name, field.Name, literal)
			continue
		}
		literal, err := objectPrimitiveLiteral(field.Type, *field.Constant)
		if err != nil {
			return unsupportedSource(class.file, field, err.Error())
		}
		fmt.Fprintf(out, "\nconst %s%s = %s\n", name, field.Name, literal)
	}
	fmt.Fprintf(out, "\ntype %s struct {\n", name)
	if class.parent != nil {
		fmt.Fprintf(out, "\tjavaBase%s\n", class.parent.declaration.Name)
	} else {
		// Empty Go structs can share an address. Java allocations must be distinct.
		out.WriteString("\t_ byte\n")
	}
	for _, field := range class.fields {
		if !slices.Contains(field.Modifiers, "STATIC") {
			fmt.Fprintf(out, "\tjavaField%s %s\n", field.Name, objectGoType(field.Type))
		}
	}
	for _, method := range class.methods {
		if method.overridden {
			parameters, _ := objectParameterList(method.declaration.Parameters)
			fmt.Fprintf(out, "\tjavaDispatch%s func(%s)%s\n", method.goName, parameters, objectResultType(method))
		}
	}
	out.WriteString("}\n")
	if class.embed {
		fmt.Fprintf(out, "\ntype javaBase%s = %s\n", name, name)
	}
	for ancestor := class.parent; ancestor != nil; ancestor = ancestor.parent {
		receiver, _ := class.memberReceiver(ancestor)
		fmt.Fprintf(out, "\nfunc (this *%s) %s() *%s {\n\tif this == nil { return nil }\n\treturn &%s\n}\n",
			name, objectViewName(ancestor), ancestor.declaration.Name, receiver)
	}

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
	return "javaCheckedResult" + class.declaration.Name
}

type javaObjectContext struct {
	program   *javaObjectProgram
	class     *javaObjectClass
	arguments map[string]string
}

func (ctx javaObjectContext) emitConstructor(out *strings.Builder, method *javaObjectMethod) error {
	name := ctx.class.declaration.Name
	declaration := method.declaration
	parameters, arguments := objectParameterList(declaration.Parameters)
	ctx.arguments = objectParameterNames(declaration.Parameters)
	if method.public {
		fmt.Fprintf(out, "\nfunc %s(%s) *%s {\n\tthis := new(%s)\n", method.goName, parameters, name, name)
		// Attach the most-derived implementations before entering any Java constructor.
		for _, methodName := range slices.Sorted(maps.Keys(ctx.class.virtual)) {
			implementation := ctx.class.virtual[methodName]
			slot := implementation.slot
			if implementation == slot {
				continue
			}
			receiver, _ := ctx.class.memberReceiver(slot.class)
			owner, _ := ctx.class.memberReceiver(implementation.class)
			fmt.Fprintf(out, "\t%s.javaDispatch%s = %s.%s\n", receiver, slot.goName, owner, implementation.internal)
		}
		fmt.Fprintf(out, "\tthis.%s(%s)\n\treturn this\n}\n", method.internal, arguments)
	}
	call := declaration.ConstructorCall
	if call == nil || (call.Receiver != "this" && call.Receiver != "super") {
		return fmt.Errorf("requires a resolved this(...) or super(...) invocation before the constructor body")
	}
	fmt.Fprintf(out, "\nfunc (this *%s) %s(%s) {\n", name, method.internal, parameters)
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
		fmt.Fprintf(out, "\t%s.%s(%s)\n", receiver, callee.internal, values)
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
	name := ctx.class.declaration.Name
	declaration := method.declaration
	parameters, arguments := objectParameterList(declaration.Parameters)
	result := objectResultType(method)
	ctx.arguments = objectParameterNames(declaration.Parameters)
	bodyName := method.internal
	if method.public && method.slot == method && !method.overridden {
		bodyName = method.goName
	}
	fmt.Fprintf(out, "\nfunc (this *%s) %s(%s)%s {\n", name, bodyName, parameters, result)
	if err := ctx.emitStatements(out, declaration.Body, declaration.Result); err != nil {
		return err
	}
	out.WriteString("}\n")
	if method.overridden {
		fmt.Fprintf(out, "\nfunc (this *%s) %s(%s)%s {\n", name, method.goName, parameters, result)
		fmt.Fprintf(out, "\tif dispatch := this.javaDispatch%s; dispatch != nil {\n", method.goName)
		if result == "" {
			fmt.Fprintf(out, "\t\tdispatch(%s)\n\t\treturn\n\t}\n\tthis.%s(%s)\n}\n", arguments, bodyName, arguments)
		} else {
			fmt.Fprintf(out, "\t\treturn dispatch(%s)\n\t}\n\treturn this.%s(%s)\n}\n", arguments, bodyName, arguments)
		}
	}
	return nil
}

func (ctx javaObjectContext) emitStatements(out *strings.Builder, body *MethodBody, result string) error {
	if body == nil {
		return fmt.Errorf("missing attributed statement sequence")
	}
	for _, statement := range body.Code {
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
			return "return", nil
		}
		if expression != nil {
			value, err := ctx.converted(expression, result)
			return "return " + value, err
		}
	case "EXPRESSION_STATEMENT":
		if expression == nil {
			break
		}
		if expression.Kind == "METHOD_INVOCATION" {
			return ctx.invoke(expression)
		}
		if expression.Kind == "ASSIGNMENT" && expression.Left != nil && expression.Left.Kind == "FIELD" &&
			!expression.Left.Static && expression.Right != nil {
			field, err := ctx.field(expression.Left)
			if err != nil {
				return "", err
			}
			value, err := ctx.converted(expression.Right, expression.Left.Type)
			return field + " = " + value, err
		}
	}
	return "", fmt.Errorf("unsupported statement %s", statement.Kind)
}

func (ctx javaObjectContext) converted(expression *JavaExpression, target string) (string, error) {
	value, err := ctx.expression(expression)
	if err != nil {
		return "", err
	}
	if target == javaStringType {
		switch expression.Type {
		case javaStringType:
			return value, nil
		case "null":
			ctx.program.runtime = true
			return "(*javart.String)(" + value + ")", nil
		}
	}
	converted, usesRuntime, err := convertPrimitive(value, expression.Type, target)
	ctx.program.runtime = ctx.program.runtime || usesRuntime
	return converted, err
}

func (ctx javaObjectContext) expression(expression *JavaExpression) (string, error) {
	switch expression.Kind {
	case "FIELD":
		return ctx.field(expression)
	case "METHOD_INVOCATION":
		return ctx.invoke(expression)
	case "NEW_CLASS":
		return ctx.newString(expression)
	case "STRING_LITERAL":
		return ctx.program.stringLiteral(expression.UTF16LE)
	case "NULL_LITERAL":
		return "nil", nil
	case "EQUAL_TO", "NOT_EQUAL_TO":
		return ctx.stringReferenceComparison(expression)
	case "PARENTHESIZED":
		value, err := ctx.expression(expression.Operand)
		return "(" + value + ")", err
	case "TYPE_CAST":
		if expression.Type == javaStringType {
			return ctx.converted(expression.Operand, expression.Type)
		}
		value, err := ctx.expression(expression.Operand)
		if err != nil {
			return "", err
		}
		// A Go conversion of a constant rejects out-of-range values that Java
		// deliberately narrows. A typed parameter keeps the conversion runtime.
		from := expression.Operand.Type
		converted, usesRuntime, err := convertPrimitive("operand", from, expression.Type)
		if err != nil {
			return "", err
		}
		ctx.program.runtime = ctx.program.runtime || usesRuntime
		return fmt.Sprintf("func(operand %s) %s { return %s }(%s)", primitiveTypes[from], primitiveTypes[expression.Type], converted, value), nil
	case "BOOLEAN_LITERAL", "CHAR_LITERAL", "INT_LITERAL", "LONG_LITERAL", "FLOAT_LITERAL", "DOUBLE_LITERAL":
		return objectPrimitiveLiteral(expression.Type, expression.Literal)
	default:
		value, usesRuntime, err := emitPrimitiveExpression(expression, ctx.arguments)
		ctx.program.runtime = ctx.program.runtime || usesRuntime
		return value, err
	}
}

func (ctx javaObjectContext) field(expression *JavaExpression) (string, error) {
	if expression.Static && expression.UTF16LE != nil && expression.Receiver != "other" {
		return ctx.program.stringLiteral(expression.UTF16LE)
	}
	if expression.Static && expression.Literal != "" && expression.Receiver != "other" {
		return objectPrimitiveLiteral(expression.Type, expression.Literal)
	}
	field, found := ctx.program.fields[expression.Symbol]
	if !found || expression.Static || (expression.Receiver != "this" && expression.Receiver != "super") {
		return "", fmt.Errorf("unsupported field access %s", expression.Symbol)
	}
	receiver, found := ctx.class.memberReceiver(field.class)
	if !found {
		return "", fmt.Errorf("field %s is outside the current class hierarchy", expression.Symbol)
	}
	return receiver + ".javaField" + field.declaration.Name, nil
}

func (ctx javaObjectContext) invoke(expression *JavaExpression) (string, error) {
	if expression.Target != nil && expression.Target.Type == javaStringType {
		return ctx.invokeString(expression)
	}
	callee := ctx.program.methods[expression.Symbol]
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

func objectParameterList(parameters []Parameter) (string, string) {
	declarations := make([]string, 0, len(parameters))
	arguments := make([]string, 0, len(parameters))
	for index, parameter := range parameters {
		name := "parameter" + strconv.Itoa(index)
		declarations = append(declarations, name+" "+objectGoType(parameter.Type))
		arguments = append(arguments, name)
	}
	return strings.Join(declarations, ", "), strings.Join(arguments, ", ")
}

func objectResultType(method *javaObjectMethod) string {
	if method.declaration.Result == "void" {
		return ""
	}
	return " " + objectGoType(method.declaration.Result)
}

func objectPrimitiveLiteral(javaType, value string) (string, error) {
	goType := primitiveTypes[javaType]
	switch javaType {
	case "boolean":
		if value == "true" || value == "false" {
			return "bool(" + value + ")", nil
		}
	case "float", "double":
		number, err := strconv.ParseFloat(value, 64)
		if err == nil && !math.IsNaN(number) && !math.IsInf(number, 0) && (number != 0 || !math.Signbit(number)) {
			return goType + "(" + value + ")", nil
		}
	case "byte", "short", "char", "int", "long":
		if integer, err := strconv.ParseInt(value, 10, 64); err == nil {
			return goType + "(" + strconv.FormatInt(integer, 10) + ")", nil
		}
	}
	return "", fmt.Errorf("unsupported %s constant %q", javaType, value)
}
