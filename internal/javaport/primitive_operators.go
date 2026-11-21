package javaport

import "fmt"

var javaPrimitiveOperators = map[string]string{
	"PLUS":               "+",
	"MINUS":              "-",
	"MULTIPLY":           "*",
	"DIVIDE":             "/",
	"REMAINDER":          "%",
	"AND":                "&",
	"OR":                 "|",
	"XOR":                "^",
	"LESS_THAN":          "<",
	"LESS_THAN_EQUAL":    "<=",
	"GREATER_THAN":       ">",
	"GREATER_THAN_EQUAL": ">=",
	"EQUAL_TO":           "==",
	"NOT_EQUAL_TO":       "!=",
	"CONDITIONAL_AND":    "&&",
	"CONDITIONAL_OR":     "||",
}

func primitiveOperandType(javaType string) string {
	if unboxed := wrapperPrimitive(javaType); unboxed != "" {
		return unboxed
	}
	return javaType
}

func promotedBinaryType(left, right string) string {
	left, right = primitiveOperandType(left), primitiveOperandType(right)
	if primitiveTypes[left] == "" || primitiveTypes[right] == "" {
		return ""
	}
	if left == "boolean" || right == "boolean" {
		if left == right {
			return "boolean"
		}
		return ""
	}
	for _, candidate := range []string{"double", "float", "long"} {
		if left == candidate || right == candidate {
			return candidate
		}
	}
	return "int"
}

func (lowering *primitiveLowering) binary(expression *JavaExpression, evaluate func(*JavaExpression) (string, error)) (string, error) {
	operator, supported := javaPrimitiveOperators[expression.Kind]
	if !supported || expression.Left == nil || expression.Right == nil {
		return "", fmt.Errorf("unsupported Java binary expression %s at %d:%d", expression.Kind, expression.Line, expression.Column)
	}
	result := primitiveTypes[expression.Type]
	promoted := promotedBinaryType(expression.Left.Type, expression.Right.Type)
	if result == "" || promoted == "" {
		return "", fmt.Errorf("unsupported Java operator %s for %s and %s", operator, expression.Left.Type, expression.Right.Type)
	}
	operand := func(source *JavaExpression) (string, error) {
		value, err := evaluate(source)
		if err != nil {
			return "", err
		}
		return lowering.convertPrimitiveContext(value, source.Type, promoted)
	}
	left, err := operand(expression.Left)
	if err != nil {
		return "", err
	}
	right, err := operand(expression.Right)
	if err != nil {
		return "", err
	}
	if operator == "&&" || operator == "||" {
		return "(" + left + " " + operator + " " + right + ")", nil
	}
	if promoted == "boolean" {
		switch operator {
		case "&":
			operator = "&&"
		case "|":
			operator = "||"
		case "^":
			operator = "!="
		}
	}
	operation := "javaLeft " + operator + " javaRight"
	switch {
	case (promoted == "int" || promoted == "long") && operator == "/":
		lowering.javaRuntime = true
		operation = "javart.DivideInteger(javaLeft, javaRight)"
	case (promoted == "int" || promoted == "long") && operator == "%":
		lowering.javaRuntime = true
		operation = "javart.RemainderInteger(javaLeft, javaRight)"
	case (promoted == "float" || promoted == "double") && operator == "%":
		lowering.mathImport = true
		operation = result + "(javamath.Mod(float64(javaLeft), float64(javaRight)))"
	case expression.Type == "float" || expression.Type == "double":
		// An explicit conversion preserves Java's rounding boundary even if
		// the Go backend can fuse arithmetic across multiple expressions.
		operation = result + "(" + operation + ")"
	}
	// Separate statements fix Java's evaluation order even when an operand
	// is a plain field read and the other operand changes that field.
	return fmt.Sprintf("func() %s {\n\tjavaLeft := %s\n\tjavaRight := %s\n\treturn %s\n}()", result, left, right, operation), nil
}
