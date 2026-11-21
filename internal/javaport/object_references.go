package javaport

import (
	"fmt"
	"maps"
	"strconv"
	"strings"
)

func (program *javaObjectProgram) emitObjectIdentity(out *strings.Builder, class *javaObjectClass, name string) {
	if class.parent == nil {
		fmt.Fprintf(out, "\nfunc (this *%s) GppJavaOwner() any { return this.javaObjectOwner }\n", name)
		fmt.Fprintf(out, "\nfunc (this *%s) GppJavaBindOwner(owner any) { this.javaObjectOwner = owner }\n", name)
	}
	fmt.Fprintf(out, "\nfunc (this *%s) GppJavaCanonicalObject() any {\n", name)
	out.WriteString("\tif this == nil { return nil }\n")
	out.WriteString("\tif owner := this.GppJavaOwner(); owner != nil { return owner }\n\treturn this\n}\n")
	if class.parent == nil {
		return
	}
	fmt.Fprintf(out, "\nfunc (this *%s) GppJavaReferenceView(target any) any {\n\tswitch target.(type) {\n", name)
	for ancestor := class.parent; ancestor != nil; ancestor = ancestor.parent {
		fmt.Fprintf(out, "\tcase %s:\n\t\treturn this.%s()\n", program.objectGoType(ancestor.declaration.Symbol), objectViewName(ancestor))
	}
	out.WriteString("\t}\n\treturn nil\n}\n")
}

func (ctx javaObjectContext) localScope() javaObjectContext {
	locals := make(map[string]string, len(ctx.locals))
	maps.Copy(locals, ctx.locals)
	ctx.locals = locals
	return ctx
}

func (ctx javaObjectContext) emitVariable(out *strings.Builder, variable JavaVariable, name string) error {
	goType := ctx.program.objectGoType(variable.Type)
	if goType == "" {
		return fmt.Errorf("unsupported Java local type %s", variable.Type)
	}
	fmt.Fprintf(out, "\tvar %s %s", name, goType)
	if variable.Initializer != nil {
		value, err := ctx.converted(variable.Initializer, variable.Type)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, " = %s", value)
	}
	fmt.Fprintf(out, "\n\t_ = %s\n", name)
	if variable.Name != "" && variable.Name != "_" {
		ctx.locals[variable.Name] = name
	}
	return nil
}

func (ctx javaObjectContext) allocateObject(expression *JavaExpression) (string, error) {
	constructor := ctx.program.methods[expression.Symbol]
	if constructor == nil || constructor.declaration.Name != "<init>" {
		return "", fmt.Errorf("unsupported Java allocation %s", expression.Symbol)
	}
	args, err := ctx.callArguments(expression, constructor)
	if err != nil {
		return "", err
	}
	return ctx.program.classReference(constructor.class, constructor.allocatorName()) + "(" + args + ")", nil
}

func (ctx javaObjectContext) invokeReference(expression *JavaExpression, method *javaObjectMethod) (string, error) {
	target, err := ctx.expression(expression.Target)
	if err != nil {
		return "", err
	}
	return ctx.emitReferenceInvocation(expression, method.declaration, target, func(receiver, arguments string) string {
		return receiver + "." + method.goName + "(" + arguments + ")"
	})
}

func (ctx javaObjectContext) emitReferenceInvocation(expression *JavaExpression, declaration Occurrence, target string, invoke func(string, string) string) (string, error) {
	parameters := declaration.Parameters
	if len(expression.Arguments) != len(parameters) {
		return "", fmt.Errorf("unsupported Java invocation arity for %s", expression.Symbol)
	}
	var code strings.Builder
	fmt.Fprintf(&code, "func()%s {\n\tjavaReceiver := %s\n", ctx.program.interfaceResult(declaration), target)
	args := make([]string, 0, len(parameters))
	for index, argument := range expression.Arguments {
		value, err := ctx.converted(argument, parameters[index].Type)
		if err != nil {
			return "", err
		}
		name := fmt.Sprintf("javaArgument%d", index)
		fmt.Fprintf(&code, "\tvar %s %s = %s\n", name, ctx.program.objectGoType(parameters[index].Type), value)
		args = append(args, name)
	}
	// Java evaluates every argument before the invocation's null-receiver check.
	code.WriteString("\tjavart.RequireNonNull(javaReceiver)\n\t")
	if declaration.Result != "void" {
		code.WriteString("return ")
	}
	fmt.Fprintf(&code, "%s\n}()", invoke("javaReceiver", strings.Join(args, ", ")))
	ctx.program.runtime = true
	return code.String(), nil
}

func (program *javaObjectProgram) referenceType(name string) bool {
	return program.mappedObjectType(name) != "" && primitiveTypes[name] == ""
}

func (program *javaObjectProgram) canConvertReference(source, target string) bool {
	if !program.referenceType(target) {
		return false
	}
	if source == target || source == "null" {
		return true
	}
	if canWidenBoxingReference(source, target) {
		return true
	}
	if _, rank := program.arrayType(source); rank != 0 {
		_, targetRank := program.arrayType(target)
		return target == javaObjectType || targetRank != 0
	}
	if target == javaObjectType {
		return program.classes[source] != nil || program.interfaces[source] != nil || source == javaAutoCloseable || javaCatchTypes[source] != ""
	}
	if iface := program.interfaces[source]; iface != nil {
		return iface.supers[target] != nil || (target == javaAutoCloseable && iface.closeable)
	}
	if class := program.classes[source]; class != nil {
		if class.interfaces[target] != nil {
			return true
		}
		if target == javaAutoCloseable && class.closeable {
			return true
		}
		for ancestor := class.parent; ancestor != nil; ancestor = ancestor.parent {
			if ancestor.declaration.Symbol == target {
				return true
			}
		}
	}
	sourceGoType := javaCatchTypes[source]
	return sourceGoType != "" && (target == "java.lang.Throwable" ||
		(target == javaErrorType && source == javaAssertionError) ||
		(target == "java.lang.Exception" && source != javaAssertionError && strings.HasPrefix(sourceGoType, "*")))
}

func (ctx javaObjectContext) convertReference(value, source, target string) (string, error) {
	if target == javaObjectType && ctx.program.canConvertReference(source, target) && source != "null" {
		return ctx.program.primitiveLowering.canonicalReference(value), nil
	}
	if boxingGoType(target) != "" {
		return ctx.program.primitiveLowering.convertBoxedReference(value, source, target)
	}
	if !ctx.program.canConvertReference(source, target) {
		return "", fmt.Errorf("unsupported Java reference conversion from %s to %s", source, target)
	}
	if source == "null" {
		return "(" + ctx.program.objectGoType(target) + ")(" + value + ")", nil
	}
	if iface := ctx.program.interfaces[target]; iface != nil {
		return ctx.interfaceReferenceCall("CastInterface", iface, value), nil
	}
	if target == javaAutoCloseable {
		ctx.program.runtime = true
		return "javart.CastReference[javart.AutoCloseable](" + value + ")", nil
	}
	if ancestor := ctx.program.classes[target]; ancestor != nil && source != target {
		return "(" + value + ")." + objectViewName(ancestor) + "()", nil
	}
	return value, nil
}

func (ctx javaObjectContext) cast(expression *JavaExpression) (string, error) {
	if expression.Operand == nil {
		return "", fmt.Errorf("missing attributed Java cast operand")
	}
	source, target := expression.Operand.Type, expression.Type
	if primitiveTypes[source] != "" || primitiveTypes[target] != "" {
		return ctx.program.primitiveLowering.cast(expression, ctx.expression)
	}
	if source != target && source != "null" && ctx.arrayElementType(target) != "" {
		return ctx.arrayTypeTest(expression.Operand, target, "CastArray")
	}
	if ctx.program.canConvertReference(source, target) {
		return ctx.converted(expression.Operand, target)
	}
	if !ctx.program.referenceType(source) || !ctx.program.referenceType(target) {
		return "", fmt.Errorf("unsupported Java reference cast from %s to %s", source, target)
	}
	value, err := ctx.expression(expression.Operand)
	if err != nil {
		return "", err
	}
	if iface := ctx.program.interfaces[target]; iface != nil {
		return ctx.interfaceReferenceCall("CastInterface", iface, value), nil
	}
	ctx.program.runtime = true
	return "javart.CastReference[" + ctx.program.objectGoType(target) + "](" + value + ")", nil
}

func (ctx javaObjectContext) instanceOf(expression *JavaExpression) (string, error) {
	if expression.Operand == nil || expression.Target == nil || expression.Target.Kind != "TYPE" {
		return "", fmt.Errorf("unsupported Java instanceof pattern")
	}
	source, target := expression.Operand.Type, expression.Target.Type
	if (source != "null" && !ctx.program.referenceType(source)) || !ctx.program.referenceType(target) {
		return "", fmt.Errorf("unsupported Java instanceof test from %s to %s", source, target)
	}
	if ctx.arrayElementType(target) != "" {
		return ctx.arrayTypeTest(expression.Operand, target, "ArrayInstanceOf")
	}
	value, err := ctx.expression(expression.Operand)
	if err != nil {
		return "", err
	}
	if iface := ctx.program.interfaces[target]; iface != nil {
		return ctx.interfaceReferenceCall("InstanceOfInterface", iface, value), nil
	}
	ctx.program.runtime = true
	return "javart.InstanceOf[" + ctx.program.objectGoType(target) + "](" + value + ")", nil
}

func (ctx javaObjectContext) referenceComparison(expression *JavaExpression) (string, error) {
	if primitiveEquality(expression) {
		return ctx.program.primitiveLowering.binary(expression, ctx.expression)
	}
	return referenceComparison(expression, ctx.program.canConvertReference, ctx.converted)
}

func referenceComparison(expression *JavaExpression, assignable func(string, string) bool, convert func(*JavaExpression, string) (string, error)) (string, error) {
	leftType, rightType := expression.Left.Type, expression.Right.Type
	equal := expression.Kind == "EQUAL_TO"
	if expression.Left.Kind == "NULL_LITERAL" && expression.Right.Kind == "NULL_LITERAL" {
		return strconv.FormatBool(equal), nil
	}
	target := leftType
	if !assignable(rightType, target) {
		target = rightType
		if !assignable(leftType, target) && assignable(leftType, javaObjectType) && assignable(rightType, javaObjectType) {
			target = javaObjectType
		}
	}
	if target == "null" {
		target = javaObjectType
	}
	comparisonOperand := func(operand *JavaExpression) (string, error) {
		if operand.Kind == "NULL_LITERAL" {
			return "nil", nil
		}
		return convert(operand, target)
	}
	left, err := comparisonOperand(expression.Left)
	if err != nil {
		return "", err
	}
	right, err := comparisonOperand(expression.Right)
	if err != nil {
		return "", err
	}
	return comparisonCode(left, right, equal), nil
}
