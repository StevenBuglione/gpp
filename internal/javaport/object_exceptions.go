package javaport

import (
	"fmt"
	"maps"
	"strings"
)

const (
	javaIOException              = "java.io.IOException"
	javaIllegalArgumentException = "java.lang.IllegalArgumentException"
	javaNullPointerException     = "java.lang.NullPointerException"
	javaAutoCloseable            = "java.lang.AutoCloseable"
	javaThrowableArray           = "java.lang.Throwable[]"
)

var javaCatchTypes = map[string]string{
	"java.lang.Throwable":                       "javart.Throwable",
	"java.lang.Exception":                       "javart.Exception",
	"java.lang.RuntimeException":                "javart.RuntimeException",
	javaIllegalArgumentException:                "*javart.IllegalArgumentException",
	javaNullPointerException:                    "*javart.NullPointerException",
	"java.lang.StringIndexOutOfBoundsException": "*javart.StringIndexOutOfBoundsException",
	javaIOException:                             "*javart.IOException",
}

func (body *MethodBody) containsTry() bool {
	if body == nil {
		return false
	}
	for _, statement := range body.Code {
		if statement.containsTry() {
			return true
		}
	}
	return false
}

func (statement *JavaStatement) containsTry() bool {
	return statement != nil && (statement.Kind == "TRY" || statement.Body.containsTry() ||
		statement.Then.containsTry() || statement.Else.containsTry())
}

func (ctx javaObjectContext) emitJavaReturnBoundary(out *strings.Builder, result string) {
	ctx.program.runtime = true
	if result == "void" {
		out.WriteString("\tvar javaResult struct{}\n")
	}
	out.WriteString("\tdefer javart.RecoverReturn(&javaResult)\n")
}

func (ctx javaObjectContext) ifStatement(statement JavaStatement, result string) (string, error) {
	if statement.Expression == nil || statement.Then == nil {
		return "", fmt.Errorf("missing attributed if condition or body")
	}
	condition, err := ctx.converted(statement.Expression, "boolean")
	if err != nil {
		return "", err
	}
	body, err := ctx.statement(*statement.Then, result)
	if err != nil {
		return "", err
	}
	code := "if " + condition + " {\n" + body + "\n}"
	if statement.Else != nil {
		alternative, err := ctx.statement(*statement.Else, result)
		if err != nil {
			return "", err
		}
		code += " else {\n" + alternative + "\n}"
	}
	return code, nil
}

func (ctx javaObjectContext) tryStatement(statement JavaStatement, result string) (string, error) {
	ctx.program.runtime = true
	ctx.inTry = true
	var code strings.Builder
	code.WriteString("javart.Try(func() {\n")
	resourceContext := ctx.localScope()
	for index, resource := range statement.Resources {
		class := ctx.program.classes[resource.Type]
		if resource.Type != javaAutoCloseable && (class == nil || !class.closeable) {
			return "", fmt.Errorf("unsupported Java resource type %s", resource.Type)
		}
		if resource.Initializer == nil {
			return "", fmt.Errorf("missing attributed Java resource initializer")
		}
		name := fmt.Sprintf("javaResource%d_%d", len(ctx.locals), index)
		if err := resourceContext.emitVariable(&code, resource, name); err != nil {
			return "", err
		}
		fmt.Fprintf(&code, "\tdefer javart.CloseResource(%s)\n", name)
	}
	if err := resourceContext.emitStatements(&code, statement.Body, result); err != nil {
		return "", err
	}
	code.WriteString("}, ")
	if len(statement.Catches) == 0 {
		code.WriteString("nil")
	} else {
		code.WriteString("func(javaFailure error) bool {\n\tswitch javaCaught := javaFailure.(type) {\n")
		for _, handler := range statement.Catches {
			if err := ctx.emitJavaCatch(&code, handler, result); err != nil {
				return "", err
			}
		}
		code.WriteString("\tdefault:\n\t\treturn false\n\t}\n}")
	}
	code.WriteString(", ")
	if statement.Finally == nil {
		code.WriteString("nil")
	} else {
		code.WriteString("func() {\n")
		if err := ctx.emitStatements(&code, statement.Finally, result); err != nil {
			return "", err
		}
		code.WriteString("}")
	}
	code.WriteString(")")
	return code.String(), nil
}

func (ctx javaObjectContext) emitJavaCatch(code *strings.Builder, handler JavaCatch, result string) error {
	bindings := make(map[string]string, len(ctx.caught)+1)
	maps.Copy(bindings, ctx.caught)
	// The switch binding is scoped to its handler. Give nested catches a
	// different Go name so a rethrow can still reference an enclosing handler.
	binding := fmt.Sprintf("javaCaught%d", len(bindings))
	bindings[handler.Name] = binding
	ctx.caught = bindings
	if len(handler.Types) == 0 {
		return fmt.Errorf("missing attributed catch type for %s", handler.Name)
	}
	// A Go type switch with several types in a case binds the original Go
	// error interface. Emit one case per Java type to retain the typed value.
	for _, javaType := range handler.Types {
		goType, ok := javaCatchTypes[javaType]
		if !ok {
			return fmt.Errorf("unsupported Java catch type %s", javaType)
		}
		fmt.Fprintf(code, "\tcase %s:\n\t\t%s := javaCaught\n\t\t_ = %s\n", goType, binding, binding)
		if err := ctx.emitStatements(code, handler.Body, result); err != nil {
			return err
		}
		code.WriteString("\t\treturn true\n")
	}
	return nil
}

func (ctx javaObjectContext) throwStatement(expression *JavaExpression) (string, error) {
	if expression == nil {
		return "", fmt.Errorf("missing attributed Java throw expression")
	}
	value := expression
	for value.Kind == "PARENTHESIZED" && value.Operand != nil {
		value = value.Operand
	}
	if _, throwable := javaCatchTypes[value.Type]; !throwable && value.Type != "null" && value.Kind != "CATCH_PARAMETER" {
		return "", fmt.Errorf("unsupported Java throw expression of type %s", value.Type)
	}
	code, err := ctx.expression(expression)
	if err != nil {
		return "", err
	}
	ctx.program.runtime = true
	return "javart.Throw(" + code + ")\n\tpanic(\"Java throw completed normally\")", nil
}

func (ctx javaObjectContext) newThrowable(expression *JavaExpression) (string, error) {
	var constructor string
	switch expression.Symbol {
	case javaIOException + "#<init>()", javaIOException + "#<init>(java.lang.String)":
		constructor = "NewIOException"
	case javaIllegalArgumentException + "#<init>()", javaIllegalArgumentException + "#<init>(java.lang.String)":
		constructor = "NewIllegalArgumentException"
	case javaNullPointerException + "#<init>()":
		ctx.program.runtime = true
		return "&javart.NullPointerException{}", nil
	default:
		return "", fmt.Errorf("unsupported Java exception constructor %s", expression.Symbol)
	}
	message := "nil"
	if len(expression.Arguments) == 1 {
		var err error
		message, err = ctx.converted(expression.Arguments[0], javaStringType)
		if err != nil {
			return "", err
		}
	}
	ctx.program.runtime = true
	return "javart." + constructor + "(" + message + ")", nil
}
