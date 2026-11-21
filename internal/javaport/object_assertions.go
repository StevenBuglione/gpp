package javaport

import (
	"fmt"
	"strconv"
	"strings"
)

type javaAssertionStatus struct {
	storage   string
	className string
}

func (status javaAssertionStatus) enabled() string {
	if status.storage == "" {
		return "javart.InterfaceAssertionsEnabled(" + strconv.Quote(status.className) + ")"
	}
	return status.storage + ".Enabled(" + strconv.Quote(status.className) + ")"
}

func isJavaAssertion(kind string) bool { return kind == "ASSERT" }

func (class *javaObjectClass) findAssertions() {
	for _, field := range class.fields {
		class.hasAssertions = class.hasAssertions || field.Initializer.containsStatement(isJavaAssertion)
	}
	for _, declaration := range class.initializers {
		class.hasAssertions = class.hasAssertions || declaration.Body.containsStatement(isJavaAssertion)
	}
	for _, declaration := range class.staticInitializers {
		class.hasAssertions = class.hasAssertions || declaration.Body.containsStatement(isJavaAssertion)
	}
	for _, methods := range [][]*javaObjectMethod{class.constructors, class.methods, class.staticMethods} {
		for _, method := range methods {
			class.hasAssertions = class.hasAssertions || method.declaration.Body.containsStatement(isJavaAssertion) ||
				method.declaration.ConstructorCall.containsStatement(isJavaAssertion)
		}
	}
}

func (class *javaObjectClass) assertionStatus() javaAssertionStatus {
	return javaAssertionStatus{
		storage:   class.storageName("javaAssertions" + class.declaration.Name),
		className: class.declaration.Symbol,
	}
}

func (iface *javaInterface) assertionStatus() javaAssertionStatus {
	return javaAssertionStatus{className: iface.declaration.Symbol}
}

func (ctx javaObjectContext) emitAssertionInitialization(out *strings.Builder) {
	if ctx.class.hasAssertions {
		fmt.Fprintf(out, "\t%s\n", ctx.class.assertionStatus().enabled())
	}
}

func (ctx javaObjectContext) assertStatement(statement JavaStatement) (string, error) {
	ctx.program.runtime = true
	var status javaAssertionStatus
	if ctx.iface != nil {
		status = ctx.iface.assertionStatus()
	} else {
		status = ctx.class.assertionStatus()
	}
	return emitJavaAssertion(statement, status,
		func(condition *JavaExpression) (string, error) { return ctx.converted(condition, "boolean") }, ctx.assertionDetail)
}

func emitJavaAssertion(statement JavaStatement, status javaAssertionStatus, conditionValue, detailValue func(*JavaExpression) (string, error)) (string, error) {
	if statement.Expression == nil {
		return "", fmt.Errorf("missing attributed Java assertion condition")
	}
	condition, err := conditionValue(statement.Expression)
	if err != nil {
		return "", err
	}
	failure := "javart.NewAssertionError()"
	if statement.Detail != nil {
		detail, err := detailValue(statement.Detail)
		if err != nil {
			return "", err
		}
		failure = "javart.NewAssertionErrorWithDetail(" + detail + ")"
	}
	// Keep the condition inside the short-circuit evaluation and the detail
	// inside the failed branch. Either can have side effects or throw.
	return "if " + status.enabled() + " && !(" + condition + ") { javart.Throw(" + failure + ") }", nil
}

func simpleAssertionDetailType(javaType string) bool {
	return primitiveTypes[javaType] != "" || wrapperPrimitive(javaType) != "" ||
		javaType == javaStringType || javaType == javaNumberType || javaType == "null"
}

func (ctx javaObjectContext) assertionDetail(expression *JavaExpression) (string, error) {
	javaType := expression.Type
	if simpleAssertionDetailType(javaType) || javaCatchTypes[javaType] != "" {
		return ctx.expression(expression)
	}
	if class := ctx.program.classes[javaType]; class != nil {
		if toString := class.virtual["ToString"]; toString != nil && len(toString.declaration.Parameters) == 0 &&
			toString.declaration.Result == javaStringType {
			return ctx.expression(expression)
		}
	}
	return "", fmt.Errorf("unsupported Java assertion detail type %s", javaType)
}

func (ctx javaObjectContext) newAssertionError(expression *JavaExpression) (string, error) {
	ctx.program.runtime = true
	constructor := strings.TrimPrefix(expression.Symbol, javaAssertionError+"#<init>")
	switch constructor {
	case "()":
		return "javart.NewAssertionError()", nil
	case "(java.lang.Object)", "(boolean)", "(char)", "(int)", "(long)", "(float)", "(double)":
		if len(expression.Arguments) != 1 {
			return "", fmt.Errorf("missing attributed AssertionError constructor argument")
		}
		value, err := ctx.assertionDetail(expression.Arguments[0])
		return "javart.NewAssertionErrorWithDetail(" + value + ")", err
	case "(java.lang.String,java.lang.Throwable)":
		if len(expression.Arguments) != 2 {
			return "", fmt.Errorf("missing attributed AssertionError message or cause")
		}
		message, err := ctx.converted(expression.Arguments[0], javaStringType)
		if err != nil {
			return "", err
		}
		cause, err := ctx.converted(expression.Arguments[1], "java.lang.Throwable")
		return "javart.NewAssertionErrorWithCause(" + message + ", " + cause + ")", err
	default:
		return "", fmt.Errorf("unsupported Java assertion error constructor %s", expression.Symbol)
	}
}
