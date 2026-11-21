package javaport

import "fmt"

var javaCompoundOperators = map[string]string{
	"PLUS_ASSIGNMENT":                 "PLUS",
	"MINUS_ASSIGNMENT":                "MINUS",
	"MULTIPLY_ASSIGNMENT":             "MULTIPLY",
	"DIVIDE_ASSIGNMENT":               "DIVIDE",
	"REMAINDER_ASSIGNMENT":            "REMAINDER",
	"AND_ASSIGNMENT":                  "AND",
	"OR_ASSIGNMENT":                   "OR",
	"XOR_ASSIGNMENT":                  "XOR",
	"LEFT_SHIFT_ASSIGNMENT":           "LEFT_SHIFT",
	"RIGHT_SHIFT_ASSIGNMENT":          "RIGHT_SHIFT",
	"UNSIGNED_RIGHT_SHIFT_ASSIGNMENT": "UNSIGNED_RIGHT_SHIFT",
}

func (ctx javaObjectContext) assignment(expression *JavaExpression, asValue bool) (string, error) {
	if target := arrayMutationTarget(expression.Left); target != nil {
		return ctx.arrayAssignment(target, expression.Right)
	}
	if expression.Right == nil {
		return "", fmt.Errorf("missing attributed Java assignment value")
	}
	left := javaMutationOperand(expression.Left)
	if left != nil && left.Kind == "FIELD" && !left.Static {
		return ctx.assignInstanceField(left, expression.Right)
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

func (ctx javaObjectContext) increment(expression *JavaExpression) (string, error) {
	operator := "PLUS"
	if expression.Kind == "PREFIX_DECREMENT" || expression.Kind == "POSTFIX_DECREMENT" {
		operator = "MINUS"
	}
	one := &JavaExpression{Kind: "INT_LITERAL", Type: "int", Literal: "1"}
	postfix := expression.Kind == "POSTFIX_INCREMENT" || expression.Kind == "POSTFIX_DECREMENT"
	return ctx.updateVariable(expression.Operand, one, operator, postfix)
}

func (ctx javaObjectContext) updateVariable(target, operand *JavaExpression, operator string, postfix bool) (string, error) {
	target = javaMutationOperand(target)
	if target == nil || operand == nil {
		return "", fmt.Errorf("missing attributed Java update target or operand")
	}
	javaType := target.Type
	var setup, load, store string
	if array := arrayMutationTarget(target); array != nil {
		value, index, component, err := ctx.arrayOperands(array)
		if err != nil {
			return "", err
		}
		setup = fmt.Sprintf("\tjavaUpdateArray := %s\n\tjavaUpdateIndex := %s\n", value, index)
		load = ctx.arrayGet("javaUpdateArray", "javaUpdateIndex", component)
		store = ctx.arraySet("javaUpdateArray", "javaUpdateIndex", component, "javaUpdateNew")
	} else if target.Kind == "FIELD" && !target.Static {
		field, err := ctx.instanceField(target)
		if err != nil {
			return "", err
		}
		setup = "\tjavaUpdateReceiver := " + field.checkedReceiver(field.receiver) + "\n"
		load = field.read("javaUpdateReceiver")
		store, err = field.write("javaUpdateReceiver", "javaUpdateNew")
		if err != nil {
			return "", err
		}
	} else {
		addressable, err := ctx.mutationTarget(target)
		if err != nil {
			return "", err
		}
		setup = "\tjavaUpdateSlot := &(" + addressable + ")\n"
		load, store = "*javaUpdateSlot", "*javaUpdateSlot = javaUpdateNew"
	}
	value, err := ctx.updatedValue(javaType, operand, operator)
	if err != nil {
		return "", err
	}
	result := "javaUpdateNew"
	if postfix {
		result = "javaUpdateOld"
	}
	// Capture the variable and save its value before evaluating the right
	// operand. An array load checks null and bounds at this point too.
	return fmt.Sprintf("func() %s {\n%s\tjavaUpdateOld := %s\n\tjavaUpdateNew := %s\n\t%s\n\treturn %s\n}()",
		ctx.program.objectGoType(javaType), setup, load, value, store, result), nil
}

func javaMutationOperand(expression *JavaExpression) *JavaExpression {
	for expression != nil && expression.Kind == "PARENTHESIZED" {
		expression = expression.Operand
	}
	return expression
}

func (ctx javaObjectContext) updatedValue(target string, operand *JavaExpression, operator string) (string, error) {
	if target == javaStringType && operator == "PLUS" {
		if kind := primitiveOperandType(operand.Type); primitiveTypes[kind] == "" && kind != javaStringType && kind != "null" {
			return "", fmt.Errorf("unsupported Java string conversion from %s", operand.Type)
		}
		value, err := ctx.expression(operand)
		if err != nil {
			return "", err
		}
		ctx.program.runtime = true
		return "javart.ConcatStringValue(javaUpdateOld, " + value + ")", nil
	}
	promoted := promotedBinaryType(target, operand.Type)
	shift := operator == "LEFT_SHIFT" || operator == "RIGHT_SHIFT" || operator == "UNSIGNED_RIGHT_SHIFT"
	if shift {
		promoted = "int"
		if primitiveOperandType(target) == "long" {
			promoted = "long"
		}
	}
	left := &JavaExpression{Type: target}
	binary := &JavaExpression{Kind: operator, Type: promoted, Left: left, Right: operand}
	evaluate := func(expression *JavaExpression) (string, error) {
		if expression == left {
			return "javaUpdateOld", nil
		}
		return ctx.expression(expression)
	}
	lowering := &ctx.program.primitiveLowering
	var value string
	var err error
	if shift {
		value, err = lowering.shift(binary, evaluate)
	} else {
		value, err = lowering.binary(binary, evaluate)
	}
	if err != nil {
		return "", err
	}
	primitive := primitiveOperandType(target)
	value, err = lowering.convert(value, promoted, primitive)
	if err != nil {
		return "", err
	}
	return ctx.convertValue(value, primitive, target)
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
	}
	return "", fmt.Errorf("unsupported Java mutation target %s", target.Kind)
}
