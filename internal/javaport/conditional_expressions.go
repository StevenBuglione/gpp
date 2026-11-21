package javaport

import "fmt"

func conditionalResultType(expression *JavaExpression) string {
	if expression.Type == "null" {
		return javaObjectType
	}
	return expression.Type
}

// A standalone conditional of null type still evaluates its condition. Give
// its generated function the required Go return type from the enclosing
// conversion so a typed reference receives a correctly typed nil.
func nullConditional(expression *JavaExpression) *JavaExpression {
	if expression == nil || expression.Type != "null" {
		return nil
	}
	for expression.Kind == "PARENTHESIZED" && expression.Operand != nil {
		expression = expression.Operand
	}
	if expression.Kind == "CONDITIONAL_EXPRESSION" {
		return expression
	}
	return nil
}

func emitJavaConditional(expression *JavaExpression, result string, goType func(string) string, convert func(*JavaExpression, string) (string, error)) (string, error) {
	if expression.Condition == nil || expression.WhenTrue == nil || expression.WhenFalse == nil {
		return "", fmt.Errorf("missing attributed Java conditional operand at %d:%d", expression.Line, expression.Column)
	}
	target := goType(result)
	if target == "" {
		return "", fmt.Errorf("unsupported Java conditional result type %s", result)
	}
	condition, err := convert(expression.Condition, "boolean")
	if err != nil {
		return "", err
	}
	whenTrue, err := convert(expression.WhenTrue, result)
	if err != nil {
		return "", err
	}
	whenFalse, err := convert(expression.WhenFalse, result)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("func() %s {\n\tif %s { return %s }\n\treturn %s\n}()", target, condition, whenTrue, whenFalse), nil
}

func (lowering *primitiveLowering) utilityConditional(expression *JavaExpression, target string, arguments map[string]string) (string, error) {
	return emitJavaConditional(expression, target, lowering.signatureType, func(operand *JavaExpression, result string) (string, error) {
		return lowering.utilityValue(operand, result, arguments)
	})
}

func (lowering *primitiveLowering) utilityValue(expression *JavaExpression, target string, arguments map[string]string) (string, error) {
	if conditional := nullConditional(expression); conditional != nil {
		return lowering.utilityConditional(conditional, target, arguments)
	}
	value, err := lowering.expression(expression, arguments)
	if err != nil {
		return "", err
	}
	return lowering.convertUtilityValue(value, expression.Type, target)
}
