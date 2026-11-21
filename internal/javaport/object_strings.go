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
	if program.supportedIntersection(javaType) {
		return "any"
	}
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

func (ctx javaObjectContext) concatString(expression *JavaExpression) (string, error) {
	if expression.UTF16LE != nil {
		return ctx.program.stringLiteral(expression.UTF16LE)
	}
	if expression.Left == nil || expression.Right == nil {
		return "", fmt.Errorf("missing attributed Java concatenation operand")
	}
	left, err := ctx.stringOperand(expression.Left)
	if err != nil {
		return "", err
	}
	right, err := ctx.stringOperand(expression.Right)
	if err != nil {
		return "", err
	}
	ctx.program.runtime = true
	// A left operand's Java toString can change the state read by the
	// right operand or throw before the right operand is evaluated.
	return fmt.Sprintf("func() *javart.String { javaConcatLeft := javart.StringForConcat(%s); return javart.ConcatStringValue(javaConcatLeft, %s) }()",
		left, right), nil
}

func (ctx javaObjectContext) stringOperand(expression *JavaExpression) (string, error) {
	if nullConditional(expression) != nil {
		return ctx.converted(expression, javaObjectType)
	}
	return ctx.expression(expression)
}

func (program *javaObjectProgram) emitObjectStringMethods(out *strings.Builder, class *javaObjectClass, name string) {
	fmt.Fprintf(out, "\nfunc (*%s) GppJavaClassName() string { return %q }\n", name, class.declaration.Symbol)
	for _, bridge := range []struct{ name, method, javaType string }{
		{"GppJavaString", "toString", javaStringType},
		{"GppJavaHashCode", "hashCode", "int"},
	} {
		goName := exportMethodName(bridge.method)
		method := class.virtual[goName]
		if method == nil || method.declaration.Name != bridge.method ||
			method.declaration.Result != bridge.javaType || len(method.declaration.Parameters) != 0 {
			continue
		}
		receiver, _ := class.memberReceiver(method.slot.class)
		fmt.Fprintf(out, "\nfunc (this *%s) %s() %s { return %s.%s() }\n",
			name, bridge.name, program.objectGoType(bridge.javaType), receiver, goName)
	}
}

func (ctx javaObjectContext) invokeObjectStringMethod(expression *JavaExpression) (string, error) {
	method := "ObjectToString"
	if expression.Symbol == "java.lang.Object#hashCode()" {
		method = "ObjectHashCode"
	}
	target := "this"
	if expression.Target != nil {
		value, err := ctx.expression(expression.Target)
		if err != nil {
			return "", err
		}
		target = value
	} else if (ctx.class == nil && ctx.iface == nil) || ctx.static {
		return "", fmt.Errorf("missing Java Object method receiver for %s", expression.Symbol)
	}
	if expression.Receiver == "super" {
		if method == "ObjectHashCode" {
			method = "ObjectIdentityHash"
		} else {
			method = "DefaultObjectString"
		}
	}
	ctx.program.runtime = true
	return "javart." + method + "(" + target + ")", nil
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
