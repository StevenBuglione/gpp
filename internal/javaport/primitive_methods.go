package javaport

import (
	"fmt"
	"go/token"
	"math"
	"slices"
	"strconv"
	"strings"
)

func emitPrimitiveUtilityClasses(report Report) (string, error) {
	packageName, err := emissionPackage(report)
	if err != nil {
		return "", err
	}
	var methods strings.Builder
	names := map[string]bool{}
	lowering := primitiveLowering{}
	for _, file := range report.Files {
		if len(file.Declarations) == 0 {
			return "", fmt.Errorf("%s: no Java utility class", file.Path)
		}
		class := file.Declarations[0]
		if class.Kind != "CLASS" || class.Owner != "" || class.AnnotationCount != 0 ||
			class.HasTypeParams || len(class.Supertypes) != 0 || !token.IsExported(class.Name) ||
			!token.IsIdentifier(class.Name) || len(class.Modifiers) != 2 ||
			!slices.Contains(class.Modifiers, "PUBLIC") || !slices.Contains(class.Modifiers, "FINAL") {
			return "", unsupportedSource(file, class, "expected one public final utility class")
		}
		hasConstructor := false
		methodCount := 0
		for _, member := range file.Declarations[1:] {
			if member.Kind != "METHOD" || member.Owner != class.Symbol || member.AnnotationCount != 0 ||
				member.HasTypeParams || len(member.Thrown) != 0 || member.Body == nil {
				return "", unsupportedSource(file, member, "unsupported utility-class member")
			}
			if member.Name == "<init>" {
				if hasConstructor || len(member.Parameters) != 0 || len(member.Body.Code) != 0 ||
					member.ConstructorCall == nil || member.ConstructorCall.Receiver != "super" ||
					member.ConstructorCall.Symbol != "java.lang.Object#<init>()" ||
					!slices.Equal(member.Modifiers, []string{"PRIVATE"}) {
					return "", unsupportedSource(file, member, "expected a private empty constructor")
				}
				hasConstructor = true
				continue
			}

			method := exportMethodName(member.Name)
			name := class.Name + method
			if !token.IsExported(method) || !token.IsIdentifier(name) || names[name] {
				return "", unsupportedSource(file, member, "overloaded or colliding exported method name")
			}
			text, err := lowering.emitMethod(name, member)
			if err != nil {
				return "", unsupportedSource(file, member, err.Error())
			}
			methods.WriteString(text)
			names[name] = true
			methodCount++
		}
		if !hasConstructor || methodCount == 0 {
			return "", unsupportedSource(file, class, "requires a private empty constructor and public static methods")
		}
	}
	if lowering.javaRuntime && packageName == "gpp.java" {
		return "", fmt.Errorf("Java runtime import conflicts with source package %q", packageName)
	}
	var source strings.Builder
	fmt.Fprintf(&source, "package %s\n", packageName)
	if lowering.javaRuntime {
		source.WriteString("\nimport javart \"gpp/java\"\n")
	}
	lowering.emitNativeImports(&source)
	source.WriteString(methods.String())
	return source.String(), nil
}

type primitiveLowering struct {
	javaRuntime bool
	mathImport  bool
}

func (lowering primitiveLowering) emitNativeImports(out *strings.Builder) {
	if lowering.mathImport {
		out.WriteString("\nimport javamath \"math\"\n")
	}
}

func (lowering *primitiveLowering) emitMethod(name string, declaration Occurrence) (string, error) {
	result := lowering.signatureType(declaration.Result)
	if result == "" || len(declaration.Modifiers) != 2 || !slices.Contains(declaration.Modifiers, "PUBLIC") ||
		!slices.Contains(declaration.Modifiers, "STATIC") ||
		len(declaration.Body.Code) != 1 || declaration.Body.Code[0].Kind != "RETURN" ||
		declaration.Body.Code[0].Expression == nil {
		return "", fmt.Errorf("expected a public static method with one supported return expression")
	}

	arguments := map[string]string{}
	parameters := make([]string, 0, len(declaration.Parameters))
	for index, parameter := range declaration.Parameters {
		goType := lowering.signatureType(parameter.Type)
		if goType == "" {
			return "", fmt.Errorf("unsupported parameter type %s", parameter.Type)
		}
		generatedName := fmt.Sprintf("parameter%d", index)
		arguments[parameter.Name] = generatedName
		parameters = append(parameters, generatedName+" "+goType)
	}

	expression := declaration.Body.Code[0].Expression
	value, err := lowering.expression(expression, arguments)
	if err != nil {
		return "", err
	}
	value, err = lowering.convertUtilityValue(value, expression.Type, declaration.Result)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("\nfunc %s(%s) %s {\n\treturn %s\n}\n", name,
		strings.Join(parameters, ", "), result, value), nil
}

func (lowering *primitiveLowering) expression(expression *JavaExpression, arguments map[string]string) (string, error) {
	evaluate := func(operand *JavaExpression) (string, error) {
		return lowering.expression(operand, arguments)
	}
	switch expression.Kind {
	case "LEFT_SHIFT", "RIGHT_SHIFT", "UNSIGNED_RIGHT_SHIFT":
		return lowering.shift(expression, evaluate)
	case "EQUAL_TO", "NOT_EQUAL_TO":
		if primitiveEquality(expression) {
			return lowering.binary(expression, evaluate)
		}
		return referenceComparison(expression, canWidenBoxingReference, func(operand *JavaExpression, target string) (string, error) {
			value, err := evaluate(operand)
			if err != nil {
				return "", err
			}
			return lowering.convertBoxedReference(value, operand.Type, target)
		})
	case "METHOD_INVOCATION", "NEW_CLASS":
		if wrapperPrimitive(expression.Type) != "" || (expression.Target != nil &&
			(wrapperPrimitive(expression.Target.Type) != "" || expression.Target.Type == javaNumberType)) {
			return lowering.wrapperCall(expression, evaluate)
		}
	case "IDENTIFIER":
		if name, ok := arguments[expression.Name]; ok {
			return name, nil
		}
	case "PARENTHESIZED":
		if expression.Operand != nil {
			value, err := evaluate(expression.Operand)
			return "(" + value + ")", err
		}
	case "TYPE_CAST":
		return lowering.cast(expression, evaluate)
	case "UNARY_PLUS", "UNARY_MINUS", "BITWISE_COMPLEMENT", "LOGICAL_COMPLEMENT":
		return lowering.unary(expression, evaluate)
	case "FIELD":
		if expression.Static && expression.Receiver != "other" && expression.Literal != "" {
			return lowering.literal(expression.Type, expression.Literal)
		}
		if value := lowering.booleanConstant(expression); value != "" {
			return value, nil
		}
	case "NULL_LITERAL":
		return "nil", nil
	case "BOOLEAN_LITERAL", "CHAR_LITERAL", "INT_LITERAL", "LONG_LITERAL", "FLOAT_LITERAL", "DOUBLE_LITERAL":
		return lowering.literal(expression.Type, expression.Literal)
	default:
		if _, binary := javaPrimitiveOperators[expression.Kind]; binary {
			return lowering.binary(expression, evaluate)
		}
	}
	return "", fmt.Errorf("unsupported expression %s at %d:%d", expression.Kind, expression.Line, expression.Column)
}

func (lowering *primitiveLowering) literal(javaType, value string) (string, error) {
	if javaType == "float" || javaType == "double" {
		width := primitiveFloatWidth(javaType)
		number, err := strconv.ParseFloat(value, width)
		if err == nil && requiresFloatingBits(number) {
			// Go constants cannot represent non-finite IEEE values or negative zero.
			lowering.mathImport = true
			if javaType == "float" {
				bits := math.Float32bits(float32(number))
				if math.IsNaN(number) {
					bits = 0x7fc00000
				}
				return fmt.Sprintf("javamath.Float32frombits(%d)", bits), nil
			}
			bits := math.Float64bits(number)
			if math.IsNaN(number) {
				bits = 0x7ff8000000000000
			}
			return fmt.Sprintf("javamath.Float64frombits(%d)", bits), nil
		}
	}
	return primitiveConstantLiteral(javaType, value)
}

func primitiveFloatWidth(javaType string) int {
	if javaType == "float" {
		return 32
	}
	return 64
}

func requiresFloatingBits(number float64) bool {
	return math.IsNaN(number) || math.IsInf(number, 0) || number == 0 && math.Signbit(number)
}

func primitiveConstantLiteral(javaType, value string) (string, error) {
	goType := primitiveTypes[javaType]
	switch javaType {
	case "boolean":
		if value == "true" || value == "false" {
			return "bool(" + value + ")", nil
		}
	case "float", "double":
		width := primitiveFloatWidth(javaType)
		if number, err := strconv.ParseFloat(value, width); err == nil && !requiresFloatingBits(number) {
			// GPP's lexer splits signed exponents. Fixed notation also lets
			// the same value be emitted as a Go const in object declarations.
			decimal := strconv.FormatFloat(number, 'f', -1, width)
			if !strings.Contains(decimal, ".") {
				decimal += ".0"
			}
			return goType + "(" + decimal + ")", nil
		}
	case "byte", "short", "char", "int", "long":
		if integer, err := strconv.ParseInt(value, 10, 64); err == nil {
			return goType + "(" + strconv.FormatInt(integer, 10) + ")", nil
		}
	}
	return "", fmt.Errorf("unsupported %s constant %q", javaType, value)
}

func (lowering *primitiveLowering) cast(expression *JavaExpression, evaluate func(*JavaExpression) (string, error)) (string, error) {
	if expression.Operand == nil {
		return "", fmt.Errorf("missing cast operand at %d:%d", expression.Line, expression.Column)
	}
	value, err := evaluate(expression.Operand)
	if err != nil {
		return "", err
	}
	from, to := expression.Operand.Type, expression.Type
	if boxingGoType(to) != "" {
		return lowering.wrapperCast(value, from, to)
	}
	if primitiveTypes[to] != "" && (from == javaObjectType || from == javaNumberType) {
		from = "java.lang." + wrapperNames[to]
		value = lowering.checkedWrapperCast(value, from)
	}
	if wrapperPrimitive(from) != "" {
		return lowering.convertPrimitiveContext(value, from, to)
	}
	return lowering.explicitPrimitiveConversion(value, from, to)
}

func (lowering *primitiveLowering) explicitPrimitiveConversion(value, from, to string) (string, error) {
	converted, err := lowering.convert("operand", from, to)
	if err != nil {
		return "", err
	}
	if from == to {
		return value, nil
	}
	// A typed value avoids Go's constant overflow diagnostics for legal Java
	// narrowing conversions. Evaluate the original expression exactly once.
	return fmt.Sprintf("func(operand %s) %s { return %s }(%s)", primitiveTypes[from], primitiveTypes[to], converted, value), nil
}

func (lowering *primitiveLowering) unary(expression *JavaExpression, evaluate func(*JavaExpression) (string, error)) (string, error) {
	if expression.Operand == nil {
		return "", fmt.Errorf("missing unary operand at %d:%d", expression.Line, expression.Column)
	}
	value, err := evaluate(expression.Operand)
	if err != nil {
		return "", err
	}
	value, err = lowering.convertPrimitiveContext(value, expression.Operand.Type, expression.Type)
	if err != nil {
		return "", err
	}
	switch expression.Kind {
	case "UNARY_PLUS":
		return "(" + value + ")", nil
	case "UNARY_MINUS":
		// Negation uses the promoted Java width and must retain negative zero.
		goType := primitiveTypes[expression.Type]
		return fmt.Sprintf("func(operand %s) %s { return -operand }(%s)", goType, goType, value), nil
	case "BITWISE_COMPLEMENT":
		return "(^" + value + ")", nil
	case "LOGICAL_COMPLEMENT":
		return "(!" + value + ")", nil
	}
	return "", fmt.Errorf("unsupported unary operator %s", expression.Kind)
}

func (lowering *primitiveLowering) shift(expression *JavaExpression, evaluate func(*JavaExpression) (string, error)) (string, error) {
	if expression.Left == nil || expression.Right == nil {
		return "", fmt.Errorf("missing shift operand at %d:%d", expression.Line, expression.Column)
	}
	// Java promotes the left operand and masks the shift distance to five
	// bits for int or six bits for long (JLS 15.19).
	mask, unsigned := 31, "uint32"
	switch expression.Type {
	case "int":
	case "long":
		mask, unsigned = 63, "uint64"
	default:
		return "", fmt.Errorf("unsupported shift result %s", expression.Type)
	}
	left, err := evaluate(expression.Left)
	if err != nil {
		return "", err
	}
	left, err = lowering.convertPrimitiveContext(left, expression.Left.Type, expression.Type)
	if err != nil {
		return "", err
	}

	right, err := evaluate(expression.Right)
	if err != nil {
		return "", err
	}
	right, rightType := lowering.unbox(right, expression.Right.Type)
	distanceType := primitiveTypes[rightType]
	if distanceType == "" {
		return "", fmt.Errorf("unsupported shift distance type %s", expression.Right.Type)
	}
	distance := fmt.Sprintf("(uint64(javaDistance) & %d)", mask)
	goType := primitiveTypes[expression.Type]
	var shifted string
	switch expression.Kind {
	case "LEFT_SHIFT":
		shifted = "javaOperand << " + distance
	case "UNSIGNED_RIGHT_SHIFT":
		shifted = goType + "(" + unsigned + "(javaOperand) >> " + distance + ")"
	default:
		shifted = "javaOperand >> " + distance
	}
	return fmt.Sprintf("func() %s {\n\tjavaOperand := %s\n\tjavaDistance := %s\n\treturn %s\n}()",
		goType, left, right, shifted), nil
}

func (lowering *primitiveLowering) convert(value, from, to string) (string, error) {
	_, validSource := primitiveTypes[from]
	target, validTarget := primitiveTypes[to]
	if !validSource || !validTarget || (from == "boolean") != (to == "boolean") {
		return "", fmt.Errorf("unsupported primitive conversion from %s to %s", from, to)
	}
	if from == to {
		return value, nil
	}
	if (from == "float" || from == "double") && to != "float" && to != "double" {
		lowering.javaRuntime = true
		if from == "float" {
			value = "float64(" + value + ")"
		}
		if to == "long" {
			return "javart.LongFromDouble(" + value + ")", nil
		}
		value = "javart.IntFromDouble(" + value + ")"
		if to == "int" {
			return value, nil
		}
	}
	return target + "(" + value + ")", nil
}
