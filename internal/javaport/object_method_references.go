package javaport

import (
	"fmt"
	"maps"
)

func (ctx javaObjectContext) methodReference(expression *JavaExpression) (string, error) {
	adapter, err := ctx.program.lambdaAdapter(expression.Type)
	if err != nil {
		return "", err
	}
	lambda := &JavaExpression{Kind: "LAMBDA_EXPRESSION", Type: expression.Type}
	invocation := &JavaExpression{Kind: "METHOD_INVOCATION", Symbol: expression.Symbol, Type: expression.Result}
	for index, parameter := range adapter.method.declaration.Parameters {
		parameter.Name = fmt.Sprintf("referenceOperand%d", index)
		lambda.Parameters = append(lambda.Parameters, parameter)
		invocation.Arguments = append(invocation.Arguments, &JavaExpression{
			Kind: "IDENTIFIER", Name: parameter.Name, Type: parameter.Type,
		})
	}
	lambda.Operand = invocation
	switch expression.Receiver {
	case "constructor":
		invocation.Kind = "NEW_CLASS"
	case "array-constructor":
		invocation.Kind = "NEW_ARRAY"
	case "static":
		invocation.Receiver = "type"
	case "unbound":
		if len(invocation.Arguments) == 0 {
			return "", fmt.Errorf("unbound Java method reference %s has no receiver parameter", expression.Symbol)
		}
		invocation.Receiver = "other"
		invocation.Target = invocation.Arguments[0]
		invocation.Arguments = invocation.Arguments[1:]
	case "this", "super", "interface-super":
		invocation.Receiver = expression.Receiver
	case "bound":
		return ctx.boundMethodReference(expression, lambda, invocation, adapter)
	default:
		return "", fmt.Errorf("unsupported Java method reference %s", expression.Symbol)
	}
	return ctx.lambda(lambda)
}

func (ctx javaObjectContext) boundMethodReference(expression, lambda, invocation *JavaExpression, adapter *javaLambdaAdapter) (string, error) {
	if expression.Target == nil {
		return "", fmt.Errorf("missing attributed receiver for Java method reference %s", expression.Symbol)
	}
	value, err := ctx.expression(expression.Target)
	if err != nil {
		return "", err
	}
	receiver := fmt.Sprintf("javaBoundReceiver%d", ctx.program.lambdaNext)
	// The receiver is evaluated and null-checked at reference creation,
	// before the synthetic closure ever receives its first invocation.
	ctx.arguments = maps.Clone(ctx.arguments)
	if ctx.arguments == nil {
		ctx.arguments = map[string]string{}
	}
	ctx.arguments["#bound"] = receiver
	invocation.Receiver = "other"
	invocation.Target = &JavaExpression{Kind: "IDENTIFIER", Name: "#bound", Type: expression.Target.Type}
	created, err := ctx.lambda(lambda)
	if err != nil {
		return "", err
	}
	ctx.program.runtime = true
	return fmt.Sprintf("func() *%s {\n\t%s := javart.RequireNonNull(%s)\n\treturn %s\n}()",
		adapter.name, receiver, value, created), nil
}
