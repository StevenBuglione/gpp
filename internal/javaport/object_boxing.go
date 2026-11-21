package javaport

import (
	"fmt"
	"strings"
)

const (
	javaObjectType = "java.lang.Object"
	javaNumberType = "java.lang.Number"
)

var wrapperNames = map[string]string{
	"boolean": "Boolean",
	"byte":    "Byte",
	"short":   "Short",
	"char":    "Character",
	"int":     "Integer",
	"long":    "Long",
	"float":   "Float",
	"double":  "Double",
}

func wrapperPrimitive(javaType string) string {
	name, ok := strings.CutPrefix(javaType, "java.lang.")
	if !ok {
		return ""
	}
	for primitive, wrapper := range wrapperNames {
		if name == wrapper {
			return primitive
		}
	}
	return ""
}

func boxingGoType(javaType string) string {
	switch javaType {
	case javaObjectType:
		return "any"
	case javaNumberType:
		return "javart.Number"
	}
	if wrapper := wrapperNames[wrapperPrimitive(javaType)]; wrapper != "" {
		return "*javart." + wrapper
	}
	return ""
}

func canWidenBoxingReference(source, target string) bool {
	if boxingGoType(target) == "" {
		return false
	}
	if source == target || source == "null" {
		return true
	}
	unboxed := wrapperPrimitive(source)
	switch target {
	case javaObjectType:
		return unboxed != "" || source == javaNumberType || source == javaStringType
	case javaNumberType:
		return unboxed != "" && unboxed != "boolean" && unboxed != "char"
	}
	return false
}

func (lowering *primitiveLowering) convertBoxedReference(value, source, target string) (string, error) {
	if source == target && target != javaObjectType && target != javaNumberType {
		return value, nil
	}
	if source == "null" {
		return "(" + lowering.signatureType(target) + ")(" + value + ")", nil
	}
	if wrapper := wrapperNames[source]; wrapper != "" {
		// A javac-validated assignment may narrow an integral constant to
		// a byte, short, or char before boxing it.
		if narrowed := wrapperPrimitive(target); (source == "int" || source == "byte" || source == "short" || source == "char") &&
			(narrowed == "byte" || narrowed == "short" || narrowed == "char") {
			var err error
			value, err = lowering.convert(value, source, narrowed)
			if err != nil {
				return "", err
			}
			source, wrapper = narrowed, wrapperNames[narrowed]
		}
		if !canWidenBoxingReference("java.lang."+wrapper, target) {
			return "", fmt.Errorf("unsupported Java boxing conversion from %s to %s", source, target)
		}
		lowering.javaRuntime = true
		return "javart.Box" + wrapper + "(" + value + ")", nil
	}
	if !canWidenBoxingReference(source, target) {
		return "", fmt.Errorf("unsupported Java reference conversion from %s to %s", source, target)
	}
	if target == javaObjectType {
		return lowering.canonicalReference(value), nil
	}
	// A Go pointer-to-interface conversion must preserve Java null.
	lowering.javaRuntime = true
	return "javart.NormalizeReference[" + boxingGoType(target) + "](" + value + ")", nil
}

func (lowering *primitiveLowering) canonicalReference(value string) string {
	lowering.javaRuntime = true
	return "javart.CanonicalReference(" + value + ")"
}

func (lowering *primitiveLowering) checkedWrapperCast(value, target string) string {
	lowering.javaRuntime = true
	return "javart.CastBoxedReference[" + boxingGoType(target) + "](" + value + ")"
}

func (lowering *primitiveLowering) unbox(value, source string) (string, string) {
	if primitive := wrapperPrimitive(source); primitive != "" {
		lowering.javaRuntime = true
		return "javart.Unbox" + wrapperNames[primitive] + "(" + value + ")", primitive
	}
	return value, source
}

func (lowering *primitiveLowering) convertPrimitiveContext(value, source, target string) (string, error) {
	value, source = lowering.unbox(value, source)
	return lowering.convert(value, source, target)
}

func (lowering *primitiveLowering) convertUtilityValue(value, source, target string) (string, error) {
	if boxingGoType(target) != "" {
		return lowering.convertBoxedReference(value, source, target)
	}
	return lowering.convertPrimitiveContext(value, source, target)
}

func (lowering *primitiveLowering) signatureType(javaType string) string {
	if goType := boxingGoType(javaType); goType != "" {
		lowering.javaRuntime = lowering.javaRuntime || strings.Contains(goType, "javart.")
		return goType
	}
	return primitiveTypes[javaType]
}

func (lowering *primitiveLowering) wrapperCast(value, source, target string) (string, error) {
	if (source == javaObjectType || source == javaNumberType) && !canWidenBoxingReference(source, target) {
		return lowering.checkedWrapperCast(value, target), nil
	}
	return lowering.convertBoxedReference(value, source, target)
}

func (lowering *primitiveLowering) wrapperCall(expression *JavaExpression, evaluate func(*JavaExpression) (string, error)) (string, error) {
	owner, method, _ := strings.Cut(expression.Symbol, "#")
	if primitive := wrapperPrimitive(owner); primitive != "" {
		switch {
		case method == "valueOf("+primitive+")" && (expression.Receiver == "type" || expression.Receiver == "this"):
			return lowering.wrapperFactory(expression, "Box"+wrapperNames[primitive], primitive, evaluate)
		case method == "<init>("+primitive+")":
			return lowering.wrapperFactory(expression, "New"+wrapperNames[primitive], primitive, evaluate)
		case owner == "java.lang.Float" && method == "<init>(double)":
			return lowering.wrapperFactory(expression, "NewFloat", "double", evaluate)
		}
	}
	if expression.Target != nil && method == expression.Type+"Value()" && len(expression.Arguments) == 0 {
		value, err := evaluate(expression.Target)
		if err != nil {
			return "", err
		}
		if expression.Target.Type == javaNumberType {
			lowering.javaRuntime = true
			return "javart.RequireNonNull(" + value + ")." + exportMethodName(method), nil
		}
		if wrapperPrimitive(expression.Target.Type) != "" {
			return lowering.convertPrimitiveContext(value, expression.Target.Type, expression.Type)
		}
	}
	return "", fmt.Errorf("unsupported Java wrapper member %s", expression.Symbol)
}

func (lowering *primitiveLowering) wrapperFactory(expression *JavaExpression, factory, primitive string, evaluate func(*JavaExpression) (string, error)) (string, error) {
	if len(expression.Arguments) != 1 {
		return "", fmt.Errorf("invalid Java wrapper factory call %s", expression.Symbol)
	}
	argument := expression.Arguments[0]
	value, err := evaluate(argument)
	if err != nil {
		return "", err
	}
	value, err = lowering.convertPrimitiveContext(value, argument.Type, primitive)
	if err != nil {
		return "", err
	}
	if factory == "NewFloat" && primitive == "double" {
		value, err = lowering.explicitPrimitiveConversion(value, "double", "float")
		if err != nil {
			return "", err
		}
	}
	lowering.javaRuntime = true
	return "javart." + factory + "(" + value + ")", nil
}

func (lowering *primitiveLowering) booleanConstant(expression *JavaExpression) string {
	if !expression.Static || expression.Receiver == "other" {
		return ""
	}
	switch expression.Symbol {
	case "java.lang.Boolean#TRUE":
		lowering.javaRuntime = true
		return "javart.BoxBoolean(true)"
	case "java.lang.Boolean#FALSE":
		lowering.javaRuntime = true
		return "javart.BoxBoolean(false)"
	}
	return ""
}

func primitiveEquality(expression *JavaExpression) bool {
	return primitiveTypes[expression.Left.Type] != "" || primitiveTypes[expression.Right.Type] != ""
}

func (lowering *primitiveLowering) primitiveComparison(expression *JavaExpression, evaluate func(*JavaExpression) (string, error)) (string, error) {
	left, err := evaluate(expression.Left)
	if err != nil {
		return "", err
	}
	right, err := evaluate(expression.Right)
	if err != nil {
		return "", err
	}
	left, leftType := lowering.unbox(left, expression.Left.Type)
	right, rightType := lowering.unbox(right, expression.Right.Type)
	promoted := "int"
	for _, candidate := range []string{"boolean", "double", "float", "long"} {
		if leftType == candidate || rightType == candidate {
			promoted = candidate
			break
		}
	}
	left, err = lowering.convert(left, leftType, promoted)
	if err != nil {
		return "", err
	}
	right, err = lowering.convert(right, rightType, promoted)
	if err != nil {
		return "", err
	}
	return comparisonCode(left, right, expression.Kind == "EQUAL_TO"), nil
}

func comparisonCode(left, right string, equal bool) string {
	operator := " == "
	if !equal {
		operator = " != "
	}
	if left == "nil" || right == "nil" {
		return "(" + left + operator + right + ")"
	}
	// Go does not require a field read to precede a later function call
	// in the same expression. Java evaluates the left operand first.
	return fmt.Sprintf("func() bool {\n\tjavaLeft := %s\n\tjavaRight := %s\n\treturn javaLeft%sjavaRight\n}()",
		left, right, operator)
}
