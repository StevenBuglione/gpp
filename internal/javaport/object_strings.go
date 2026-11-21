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
	if iface := program.interfaces[javaType]; iface != nil {
		return program.interfaceReference(iface, iface.declaration.Name)
	}
	goType := program.mappedObjectType(javaType)
	program.runtime = program.runtime || strings.Contains(goType, "javart.")
	return goType
}

func (program *javaObjectProgram) mappedObjectType(javaType string) string {
	if goType := boxingGoType(javaType); goType != "" {
		return goType
	}
	if javaType == javaStringType {
		return "*javart.String"
	}
	if javaType == javaRecordType {
		return "javart.Record"
	}
	if class := program.classes[javaType]; class != nil {
		return "*" + class.declaration.Name
	}
	if iface := program.interfaces[javaType]; iface != nil {
		return iface.declaration.Name
	}
	if javaType == javaAutoCloseable {
		return "javart.AutoCloseable"
	}
	if _, rank := program.arrayType(javaType); rank != 0 {
		return "*javart.Array"
	}
	if goType := javaCatchTypes[javaType]; goType != "" {
		return goType
	}
	return primitiveTypes[javaType]
}

func (program *javaObjectProgram) stringLiteral(encoded *string) (string, error) {
	program.runtime = true
	return javaStringLiteral(encoded)
}

func javaStringLiteral(encoded *string) (string, error) {
	if encoded == nil {
		return "", fmt.Errorf("missing attributed Java String constant")
	}
	data, err := hex.DecodeString(*encoded)
	if err != nil || len(data)%2 != 0 {
		return "", fmt.Errorf("invalid UTF-16 code units in Java String constant")
	}
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
		return ctx.checkedRuntimeCall("javart.StringCopy("+value+")", javaStringType), nil
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
	return ctx.checkedRuntimeCall(call, expression.Type), nil
}

func (ctx javaObjectContext) checkedRuntimeCall(call, resultType string) string {
	helper := ""
	if ctx.iface != nil {
		ctx.iface.checksErrors = true
		helper = ctx.iface.checkedResultName()
	} else {
		ctx.class.checksErrors = true
		helper = ctx.class.checkedResultName()
	}
	if ctx.iface != nil || ctx.static {
		return fmt.Sprintf("func() %s {\n\tjavaValue, javaFailure := %s\n\treturn %s(javaValue, javaFailure)\n}()",
			ctx.program.objectGoType(resultType), call, helper)
	}
	return helper + "(" + call + ")"
}
