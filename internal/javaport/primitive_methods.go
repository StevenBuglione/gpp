package javaport

import (
	"fmt"
	"go/token"
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
	usesJavaRuntime := false
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
			text, usesRuntime, err := emitPrimitiveMethod(name, member)
			if err != nil {
				return "", unsupportedSource(file, member, err.Error())
			}
			methods.WriteString(text)
			usesJavaRuntime = usesJavaRuntime || usesRuntime
			names[name] = true
			methodCount++
		}
		if !hasConstructor || methodCount == 0 {
			return "", unsupportedSource(file, class, "requires a private empty constructor and public static methods")
		}
	}
	if usesJavaRuntime {
		if packageName == "gpp.java" {
			return "", fmt.Errorf("Java runtime import conflicts with source package %q", packageName)
		}
		return "package " + packageName + "\n\nimport javart \"gpp/java\"\n" + methods.String(), nil
	}
	return "package " + packageName + "\n" + methods.String(), nil
}

func emitPrimitiveMethod(name string, declaration Occurrence) (string, bool, error) {
	result, ok := primitiveTypes[declaration.Result]
	if !ok || len(declaration.Modifiers) != 2 || !slices.Contains(declaration.Modifiers, "PUBLIC") ||
		!slices.Contains(declaration.Modifiers, "STATIC") ||
		len(declaration.Body.Code) != 1 || declaration.Body.Code[0].Kind != "RETURN" ||
		declaration.Body.Code[0].Expression == nil {
		return "", false, fmt.Errorf("expected a public static primitive method with one return expression")
	}

	arguments := map[string]string{}
	parameters := make([]string, 0, len(declaration.Parameters))
	for index, parameter := range declaration.Parameters {
		goType, ok := primitiveTypes[parameter.Type]
		if !ok {
			return "", false, fmt.Errorf("unsupported parameter type %s", parameter.Type)
		}
		generatedName := fmt.Sprintf("parameter%d", index)
		arguments[parameter.Name] = generatedName
		parameters = append(parameters, generatedName+" "+goType)
	}

	expression := declaration.Body.Code[0].Expression
	value, usesRuntime, err := emitPrimitiveExpression(expression, arguments)
	if err != nil {
		return "", false, err
	}
	value, convertedWithRuntime, err := convertPrimitive(value, expression.Type, declaration.Result)
	if err != nil {
		return "", false, err
	}
	return fmt.Sprintf("\nfunc %s(%s) %s {\n\treturn %s\n}\n", name,
		strings.Join(parameters, ", "), result, value), usesRuntime || convertedWithRuntime, nil
}

func emitPrimitiveExpression(expression *JavaExpression, arguments map[string]string) (string, bool, error) {
	switch expression.Kind {
	case "LEFT_SHIFT", "RIGHT_SHIFT", "UNSIGNED_RIGHT_SHIFT":
		return emitPrimitiveShift(expression, arguments)
	case "IDENTIFIER":
		if name, ok := arguments[expression.Name]; ok {
			return name, false, nil
		}
	case "PARENTHESIZED", "TYPE_CAST":
		if expression.Operand == nil {
			break
		}
		value, usesRuntime, err := emitPrimitiveExpression(expression.Operand, arguments)
		if err != nil {
			return "", false, err
		}
		if expression.Kind == "PARENTHESIZED" {
			return "(" + value + ")", usesRuntime, nil
		}
		value, convertedWithRuntime, err := convertPrimitive(value, expression.Operand.Type, expression.Type)
		return value, usesRuntime || convertedWithRuntime, err
	}
	return "", false, fmt.Errorf("unsupported expression %s at %d:%d", expression.Kind, expression.Line, expression.Column)
}

func emitPrimitiveShift(expression *JavaExpression, arguments map[string]string) (string, bool, error) {
	if expression.Left == nil || expression.Right == nil {
		return "", false, fmt.Errorf("missing shift operand at %d:%d", expression.Line, expression.Column)
	}
	// Java promotes the left operand and masks the shift distance to five
	// bits for int or six bits for long (JLS 15.19).
	mask, unsigned := uint64(31), "uint32"
	switch expression.Type {
	case "int":
	case "long":
		mask, unsigned = 63, "uint64"
	default:
		return "", false, fmt.Errorf("unsupported shift result %s", expression.Type)
	}
	left, usesRuntime, err := emitPrimitiveExpression(expression.Left, arguments)
	if err != nil {
		return "", false, err
	}
	left, convertedWithRuntime, err := convertPrimitive(left, expression.Left.Type, expression.Type)
	if err != nil {
		return "", false, err
	}
	usesRuntime = usesRuntime || convertedWithRuntime

	var distance string
	if expression.Right.Kind == "INT_LITERAL" || expression.Right.Kind == "LONG_LITERAL" {
		literal, err := strconv.ParseInt(expression.Right.Literal, 10, 64)
		if err != nil {
			return "", false, fmt.Errorf("invalid shift distance %q", expression.Right.Literal)
		}
		distance = strconv.FormatUint(uint64(literal)&mask, 10)
	} else {
		right, rightUsesRuntime, err := emitPrimitiveExpression(expression.Right, arguments)
		if err != nil {
			return "", false, err
		}
		usesRuntime = usesRuntime || rightUsesRuntime
		distance = fmt.Sprintf("(uint64(%s) & %d)", right, mask)
	}
	switch expression.Kind {
	case "LEFT_SHIFT":
		return "(" + left + " << " + distance + ")", usesRuntime, nil
	case "UNSIGNED_RIGHT_SHIFT":
		return primitiveTypes[expression.Type] + "(" + unsigned + "(" + left + ") >> " + distance + ")", usesRuntime, nil
	default:
		return "(" + left + " >> " + distance + ")", usesRuntime, nil
	}
}

func convertPrimitive(value, from, to string) (string, bool, error) {
	_, validSource := primitiveTypes[from]
	target, validTarget := primitiveTypes[to]
	if !validSource || !validTarget || (from == "boolean") != (to == "boolean") {
		return "", false, fmt.Errorf("unsupported primitive conversion from %s to %s", from, to)
	}
	if from == to {
		return value, false, nil
	}
	if from == "float" || from == "double" {
		if to == "float" || to == "double" {
			return target + "(" + value + ")", false, nil
		}
		if from == "float" {
			value = "float64(" + value + ")"
		}
		if to == "long" {
			return "javart.LongFromDouble(" + value + ")", true, nil
		}
		value = "javart.IntFromDouble(" + value + ")"
		if to == "int" {
			return value, true, nil
		}
		return target + "(" + value + ")", true, nil
	}
	return target + "(" + value + ")", false, nil
}
