package javaport

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

const javaStringType = "java.lang.String"

func (program *javaObjectProgram) objectGoType(javaType string) string {
	if class := program.classes[javaType]; class != nil {
		return "*" + program.classReference(class, class.declaration.Name)
	}
	goType := program.mappedObjectType(javaType)
	program.runtime = program.runtime || strings.Contains(goType, "javart.")
	return goType
}

func (program *javaObjectProgram) mappedObjectType(javaType string) string {
	if javaType == javaStringType {
		return "*javart.String"
	}
	if class := program.classes[javaType]; class != nil {
		return "*" + class.declaration.Name
	}
	if javaType == javaAutoCloseable {
		return "javart.AutoCloseable"
	}
	if javaType == javaThrowableArray {
		return "[]javart.Throwable"
	}
	if goType := javaCatchTypes[javaType]; goType != "" {
		return goType
	}
	return primitiveTypes[javaType]
}

func (program *javaObjectProgram) stringLiteral(encoded *string) (string, error) {
	if encoded == nil {
		return "", fmt.Errorf("missing attributed Java String constant")
	}
	data, err := hex.DecodeString(*encoded)
	if err != nil || len(data)%2 != 0 {
		return "", fmt.Errorf("invalid UTF-16 code units in Java String constant")
	}
	program.runtime = true
	return "javart.InternStringLiteral(" + strconv.Quote(string(data)) + ")", nil
}

func (ctx javaObjectContext) newString(expression *JavaExpression) (string, error) {
	switch expression.Symbol {
	case "java.lang.String#<init>()":
		ctx.program.runtime = true
		return "javart.StringFromUTF16(nil)", nil
	case "java.lang.String#<init>(java.lang.String)":
		value, err := ctx.converted(expression.Arguments[0], javaStringType)
		if err != nil {
			return "", err
		}
		ctx.program.runtime = true
		return ctx.checkedRuntimeCall("javart.StringCopy(" + value + ")"), nil
	default:
		return "", fmt.Errorf("unsupported Java allocation %s", expression.Symbol)
	}
}

var stringInvocations = map[string]struct {
	name       string
	parameters []string
}{
	"java.lang.String#length()":                 {name: "StringLength"},
	"java.lang.String#charAt(int)":              {name: "StringCharAt", parameters: []string{"int"}},
	"java.lang.String#equals(java.lang.Object)": {name: "StringEquals", parameters: []string{javaStringType}},
	"java.lang.String#hashCode()":               {name: "StringHashCode"},
	"java.lang.String#intern()":                 {name: "StringIntern"},
}

func (ctx javaObjectContext) invokeString(expression *JavaExpression) (string, error) {
	method, found := stringInvocations[expression.Symbol]
	if !found || len(expression.Arguments) != len(method.parameters) {
		return "", fmt.Errorf("unsupported Java String method %s", expression.Symbol)
	}
	target, err := ctx.converted(expression.Target, javaStringType)
	if err != nil {
		return "", err
	}
	arguments := []string{target}
	for index, argument := range expression.Arguments {
		value, err := ctx.converted(argument, method.parameters[index])
		if err != nil {
			return "", err
		}
		arguments = append(arguments, value)
	}
	ctx.program.runtime = true
	call := "javart." + method.name + "(" + strings.Join(arguments, ", ") + ")"
	return ctx.checkedRuntimeCall(call), nil
}

func (ctx javaObjectContext) checkedRuntimeCall(call string) string {
	ctx.class.checksErrors = true
	return ctx.class.checkedResultName() + "(" + call + ")"
}
