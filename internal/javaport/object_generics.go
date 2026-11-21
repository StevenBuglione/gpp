package javaport

import (
	"fmt"
	"slices"
)

// Imported declarations use their JVM-erased types for storage and Go-facing
// signatures. The analysis report retains the source signatures.
func eraseJavaObjectDeclaration(declaration Occurrence) Occurrence {
	if declaration.ErasedSupertypes != nil {
		declaration.Supertypes = declaration.ErasedSupertypes
	}
	if declaration.ErasedType != "" {
		declaration.Type = declaration.ErasedType
	}
	if declaration.ErasedResult != "" {
		declaration.Result = declaration.ErasedResult
	}
	declaration.Parameters = eraseJavaParameters(declaration.Parameters)
	declaration.RecordComponents = eraseJavaParameters(declaration.RecordComponents)
	return declaration
}

func eraseJavaParameters(parameters []Parameter) []Parameter {
	if !slices.ContainsFunc(parameters, func(parameter Parameter) bool { return parameter.ErasedType != "" }) {
		return parameters
	}
	parameters = slices.Clone(parameters)
	for index := range parameters {
		parameter := &parameters[index]
		if parameter.ErasedType != "" {
			parameter.Type, parameter.ErasedType = parameter.ErasedType, ""
		}
	}
	return parameters
}

func (ctx javaObjectContext) erasedReadType(expression *JavaExpression) string {
	expression = javaMutationOperand(expression)
	switch expression.Kind {
	case "METHOD_INVOCATION":
		if method := ctx.program.methods[expression.Symbol]; method != nil {
			return method.declaration.Result
		}
		if method := ctx.program.interfaceMethods[expression.Symbol]; method != nil {
			return method.declaration.Result
		}
	case "FIELD":
		if field, ok := ctx.program.fields[expression.Symbol]; ok {
			return field.declaration.Type
		}
	}
	return expression.Type
}

func (ctx javaObjectContext) canUseErasedRead(erased, target string) bool {
	if ctx.arrayElementType(erased) != "" {
		return erased == target || target == javaObjectType
	}
	return ctx.program.canConvertReference(erased, target)
}

func (ctx javaObjectContext) invocationArgument(call *JavaExpression, index int, parameter string) (string, error) {
	argument := call.Arguments[index]
	if len(call.InvocationParameters) == 0 || call.InvocationParameters[index] == parameter {
		return ctx.converted(argument, parameter)
	}
	instantiated := call.InvocationParameters[index]
	value, err := ctx.converted(argument, instantiated)
	if err != nil {
		return "", err
	}
	return ctx.convertValue(value, instantiated, parameter)
}

func javaInvocationArity(call *JavaExpression, parameters []Parameter) bool {
	return len(call.Arguments) == len(parameters) &&
		(len(call.InvocationParameters) == 0 || len(call.InvocationParameters) == len(parameters))
}

func (ctx javaObjectContext) referenceTestValue(expression *JavaExpression) (string, error) {
	if ctx.erasedReadType(expression) != expression.Type {
		return ctx.converted(expression, javaObjectType)
	}
	return ctx.expression(expression)
}

func (ctx javaObjectContext) adaptErasedRead(value, erased, target string) (string, error) {
	if erased == target {
		return value, nil
	}
	if ctx.canUseErasedRead(erased, target) {
		return ctx.convertValue(value, erased, target)
	}
	if !ctx.program.referenceType(erased) || !ctx.program.referenceType(target) {
		return "", fmt.Errorf("unsupported Java erased result conversion from %s to %s", erased, target)
	}
	ctx.program.runtime = true
	if ctx.arrayElementType(target) != "" {
		typeOf, err := ctx.arrayTypeCall(target, "ArrayTypeOf")
		if err != nil {
			return "", err
		}
		return "javart.CastArray(" + value + ", " + typeOf + ")", nil
	}
	if iface := ctx.program.interfaces[target]; iface != nil {
		return ctx.interfaceReferenceCall("CastInterface", iface, value), nil
	}
	if ctx.program.supportedIntersection(target) {
		return ctx.checkIntersection(value, target), nil
	}
	return "javart.CastReference[" + ctx.program.objectGoType(target) + "](" + value + ")", nil
}
