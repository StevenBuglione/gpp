package javaport

import (
	"fmt"
	"maps"
	"strconv"
	"strings"
)

func (ctx javaObjectContext) localScope() javaObjectContext {
	locals := make(map[string]string, len(ctx.locals))
	maps.Copy(locals, ctx.locals)
	ctx.locals = locals
	return ctx
}

func (ctx javaObjectContext) emitVariable(out *strings.Builder, variable JavaVariable, name string) error {
	goType := ctx.program.objectGoType(variable.Type)
	if goType == "" {
		return fmt.Errorf("unsupported Java local type %s", variable.Type)
	}
	fmt.Fprintf(out, "\tvar %s %s", name, goType)
	if variable.Initializer != nil {
		value, err := ctx.converted(variable.Initializer, variable.Type)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, " = %s", value)
	}
	fmt.Fprintf(out, "\n\t_ = %s\n", name)
	if variable.Name != "" && variable.Name != "_" {
		ctx.locals[variable.Name] = name
	}
	return nil
}

func (ctx javaObjectContext) allocateObject(expression *JavaExpression) (string, error) {
	constructor := ctx.program.methods[expression.Symbol]
	if constructor == nil || constructor.declaration.Name != "<init>" {
		return "", fmt.Errorf("unsupported Java allocation %s", expression.Symbol)
	}
	args, err := ctx.callArguments(expression, constructor)
	if err != nil {
		return "", err
	}
	return ctx.program.classReference(constructor.class, constructor.allocatorName()) + "(" + args + ")", nil
}

func (ctx javaObjectContext) invokeReference(expression *JavaExpression, method *javaObjectMethod) (string, error) {
	parameters := method.declaration.Parameters
	if len(expression.Arguments) != len(parameters) {
		return "", fmt.Errorf("unsupported Java invocation arity for %s", expression.Symbol)
	}
	target, err := ctx.expression(expression.Target)
	if err != nil {
		return "", err
	}
	var code strings.Builder
	fmt.Fprintf(&code, "func()%s {\n\tjavaReceiver := %s\n", ctx.program.objectResultType(method), target)
	args := make([]string, 0, len(parameters))
	for index, argument := range expression.Arguments {
		value, err := ctx.converted(argument, parameters[index].Type)
		if err != nil {
			return "", err
		}
		name := fmt.Sprintf("javaArgument%d", index)
		fmt.Fprintf(&code, "\tvar %s %s = %s\n", name, ctx.program.objectGoType(parameters[index].Type), value)
		args = append(args, name)
	}
	// Java evaluates every argument before the invocation's null-receiver check.
	code.WriteString("\tjavart.RequireNonNull(javaReceiver)\n\t")
	if method.declaration.Result != "void" {
		code.WriteString("return ")
	}
	fmt.Fprintf(&code, "javaReceiver.%s(%s)\n}()", method.goName, strings.Join(args, ", "))
	ctx.program.runtime = true
	return code.String(), nil
}

func (program *javaObjectProgram) referenceType(name string) bool {
	return program.mappedObjectType(name) != "" && primitiveTypes[name] == ""
}

func (program *javaObjectProgram) canConvertReference(source, target string) bool {
	if !program.referenceType(target) {
		return false
	}
	if source == target || source == "null" {
		return true
	}
	if canWidenBoxingReference(source, target) {
		return true
	}
	if class := program.classes[source]; class != nil {
		if target == javaAutoCloseable && class.closeable {
			return true
		}
		for ancestor := class.parent; ancestor != nil; ancestor = ancestor.parent {
			if ancestor.declaration.Symbol == target {
				return true
			}
		}
	}
	sourceGoType := javaCatchTypes[source]
	return sourceGoType != "" && (target == "java.lang.Throwable" ||
		(target == "java.lang.Exception" && strings.HasPrefix(sourceGoType, "*")))
}

func (ctx javaObjectContext) convertReference(value, source, target string) (string, error) {
	if boxingGoType(target) != "" {
		return ctx.program.primitiveLowering.convertBoxedReference(value, source, target)
	}
	if !ctx.program.canConvertReference(source, target) {
		return "", fmt.Errorf("unsupported Java reference conversion from %s to %s", source, target)
	}
	if source == "null" {
		return "(" + ctx.program.objectGoType(target) + ")(" + value + ")", nil
	}
	if target == javaAutoCloseable {
		ctx.program.runtime = true
		return "javart.AsAutoCloseable(" + value + ")", nil
	}
	if ancestor := ctx.program.classes[target]; ancestor != nil && source != target {
		return "(" + value + ")." + objectViewName(ancestor) + "()", nil
	}
	return value, nil
}

func (ctx javaObjectContext) referenceComparison(expression *JavaExpression) (string, error) {
	if primitiveEquality(expression) {
		return ctx.program.primitiveLowering.primitiveComparison(expression, ctx.expression)
	}
	return referenceComparison(expression, ctx.program.canConvertReference, ctx.converted)
}

func referenceComparison(expression *JavaExpression, assignable func(string, string) bool, convert func(*JavaExpression, string) (string, error)) (string, error) {
	leftType, rightType := expression.Left.Type, expression.Right.Type
	equal := expression.Kind == "EQUAL_TO"
	if leftType == "null" && rightType == "null" {
		return strconv.FormatBool(equal), nil
	}
	if leftType == javaThrowableArray && rightType == javaThrowableArray {
		return "", fmt.Errorf("Java array identity comparison requires a native array reference")
	}
	target := leftType
	if !assignable(rightType, target) {
		target = rightType
	}
	comparisonOperand := func(operand *JavaExpression) (string, error) {
		if operand.Type == "null" {
			return "nil", nil
		}
		return convert(operand, target)
	}
	left, err := comparisonOperand(expression.Left)
	if err != nil {
		return "", err
	}
	right, err := comparisonOperand(expression.Right)
	if err != nil {
		return "", err
	}
	return comparisonCode(left, right, equal), nil
}

func (ctx javaObjectContext) integerArithmetic(expression *JavaExpression) (string, error) {
	if expression.Type != "int" && expression.Type != "long" {
		return "", fmt.Errorf("unsupported Java operator %s for %s", expression.Kind, expression.Type)
	}
	left, err := ctx.converted(expression.Left, expression.Type)
	if err != nil {
		return "", err
	}
	right, err := ctx.converted(expression.Right, expression.Type)
	if err != nil {
		return "", err
	}
	operator := "+"
	if expression.Kind == "MULTIPLY" {
		operator = "*"
	}
	return fmt.Sprintf("func() %s {\n\tjavaLeft := %s\n\tjavaRight := %s\n\treturn javaLeft %s javaRight\n}()",
		primitiveTypes[expression.Type], left, right, operator), nil
}
