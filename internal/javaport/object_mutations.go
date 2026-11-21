package javaport

import "fmt"

func (ctx javaObjectContext) assignment(expression *JavaExpression, asValue bool) (string, error) {
	if target := arrayAssignmentTarget(expression); target != nil {
		return ctx.arrayAssignment(target, expression.Right)
	}
	if expression.Right == nil {
		return "", fmt.Errorf("missing attributed Java assignment value")
	}
	target, err := ctx.mutationTarget(expression.Left)
	if err != nil {
		return "", err
	}
	javaType := expression.Left.Type
	value, err := ctx.converted(expression.Right, javaType)
	if err != nil {
		return "", err
	}
	if !asValue {
		return target + " = " + value, nil
	}
	goType := ctx.program.objectGoType(javaType)
	return fmt.Sprintf("func(destination *%s, value %s) %s { *destination = value; return value }(&%s, %s)",
		goType, goType, goType, target, value), nil
}

func (ctx javaObjectContext) increment(expression *JavaExpression, asValue bool) (string, error) {
	target, err := ctx.mutationTarget(expression.Operand)
	if err != nil {
		return "", err
	}
	goType := primitiveTypes[expression.Operand.Type]
	if goType == "" || expression.Operand.Type == "boolean" {
		return "", fmt.Errorf("unsupported Java increment or decrement type %s", expression.Operand.Type)
	}
	operator := "++"
	if expression.Kind == "PREFIX_DECREMENT" || expression.Kind == "POSTFIX_DECREMENT" {
		operator = "--"
	}
	if !asValue {
		return target + operator, nil
	}
	if expression.Kind == "POSTFIX_INCREMENT" || expression.Kind == "POSTFIX_DECREMENT" {
		return fmt.Sprintf("func(destination *%s) %s { value := *destination; (*destination)%s; return value }(&%s)",
			goType, goType, operator, target), nil
	}
	return fmt.Sprintf("func(destination *%s) %s { (*destination)%s; return *destination }(&%s)",
		goType, goType, operator, target), nil
}

func (ctx javaObjectContext) mutationTarget(target *JavaExpression) (string, error) {
	if target == nil {
		return "", fmt.Errorf("missing attributed Java mutation target")
	}
	switch target.Kind {
	case "PARENTHESIZED":
		return ctx.mutationTarget(target.Operand)
	case "IDENTIFIER", "LOCAL_VARIABLE", "CATCH_PARAMETER":
		return ctx.expression(target)
	case "FIELD":
		if !target.Static {
			return ctx.field(target)
		}
	}
	return "", fmt.Errorf("unsupported Java mutation target %s", target.Kind)
}
