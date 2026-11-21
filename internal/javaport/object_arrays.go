package javaport

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

func (program *javaObjectProgram) arrayType(javaType string) (leaf string, rank int) {
	for strings.HasSuffix(javaType, "[]") {
		javaType = strings.TrimSuffix(javaType, "[]")
		rank++
	}
	if rank == 0 || program.mappedObjectType(javaType) == "" {
		return "", 0
	}
	return javaType, rank
}

func (ctx javaObjectContext) arrayElementType(javaType string) string {
	if _, rank := ctx.program.arrayType(javaType); rank != 0 {
		return strings.TrimSuffix(javaType, "[]")
	}
	return ""
}

func (ctx javaObjectContext) arrayLength(value string) string {
	ctx.program.runtime = true
	return "javart.ArrayLength(" + value + ")"
}

func (ctx javaObjectContext) arrayGet(value, index, componentType string) string {
	ctx.program.runtime = true
	return "javart.ArrayGet[" + ctx.program.objectGoType(componentType) + "](" + value + ", " + index + ")"
}

func (ctx javaObjectContext) arraySet(value, index, componentType, element string) string {
	ctx.program.runtime = true
	return "javart.ArraySet[" + ctx.program.objectGoType(componentType) + "](" + value + ", " + index + ", " + element + ")"
}

// arrayTypeCall emits a typed runtime descriptor or allocation. Unlike Go
// pointers, ordinary Java class arrays can widen to arrays of ancestor types.
func (ctx javaObjectContext) arrayTypeCall(javaType, method string, arguments ...string) (string, error) {
	leaf, rank := ctx.program.arrayType(javaType)
	if rank == 0 {
		return "", fmt.Errorf("unsupported Java array type %s", javaType)
	}
	args := append([]string{strconv.Itoa(rank)}, arguments...)
	args = append(args, "javart.ArrayComponentDescriptor("+strconv.Quote(javaArrayComponentDescriptor(leaf))+")")
	if class := ctx.program.classes[leaf]; class != nil {
		for ancestor := class.parent; ancestor != nil; ancestor = ancestor.parent {
			args = append(args, "("+ctx.program.objectGoType(ancestor.declaration.Symbol)+")(nil)")
		}
	}
	if iface := ctx.program.interfaces[leaf]; iface != nil {
		supers := slices.Sorted(maps.Keys(iface.supers))
		for index, name := range supers {
			supers[index] = strconv.Quote(name)
		}
		args = append(args, "javart.ArrayInterfaceType{Name: "+strconv.Quote(leaf)+", Supers: []string{"+strings.Join(supers, ", ")+"}}")
	}
	ctx.program.runtime = true
	return "javart." + method + "[" + ctx.program.objectGoType(leaf) + "](" + strings.Join(args, ", ") + ")", nil
}

func javaArrayComponentDescriptor(leaf string) string {
	if descriptor := map[string]string{
		"boolean": "Z", "byte": "B", "short": "S", "char": "C",
		"int": "I", "long": "J", "float": "F", "double": "D",
	}[leaf]; descriptor != "" {
		return descriptor
	}
	return "L" + leaf + ";"
}

func (ctx javaObjectContext) arrayTypeTest(operand *JavaExpression, javaType, method string) (string, error) {
	descriptor, err := ctx.arrayTypeCall(javaType, "ArrayTypeOf")
	if err != nil {
		return "", err
	}
	read := ctx.expression
	if method == "ArrayInstanceOf" {
		read = ctx.referenceTestValue
	}
	value, err := read(operand)
	if err != nil {
		return "", err
	}
	return "javart." + method + "(" + value + ", " + descriptor + ")", nil
}

func (ctx javaObjectContext) newArray(expression *JavaExpression) (string, error) {
	var code strings.Builder
	code.WriteString("func() *javart.Array {\n")
	if expression.Kind == "ARRAY_INITIALIZER" {
		allocated, err := ctx.arrayTypeCall(expression.Type, "NewArray", "[]int32{"+strconv.Itoa(len(expression.Arguments))+"}")
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&code, "\tjavaArray := %s\n", allocated)
		component := ctx.arrayElementType(expression.Type)
		for index, initializer := range expression.Arguments {
			value, err := ctx.converted(initializer, component)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&code, "\t%s\n", ctx.arraySet("javaArray", strconv.Itoa(index), component, value))
		}
		code.WriteString("\treturn javaArray\n}()")
		return code.String(), nil
	}

	_, rank := ctx.program.arrayType(expression.Type)
	if len(expression.Arguments) == 0 || len(expression.Arguments) > rank {
		return "", fmt.Errorf("unsupported Java array dimensions for %s", expression.Type)
	}
	dimensions := make([]string, 0, len(expression.Arguments))
	for index, dimension := range expression.Arguments {
		value, err := ctx.converted(dimension, "int")
		if err != nil {
			return "", err
		}
		name := "javaDimension" + strconv.Itoa(index)
		fmt.Fprintf(&code, "\t%s := %s\n", name, value)
		dimensions = append(dimensions, name)
	}
	allocated, err := ctx.arrayTypeCall(expression.Type, "NewArray", "[]int32{"+strings.Join(dimensions, ", ")+"}")
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&code, "\treturn %s\n}()", allocated)
	return code.String(), nil
}

func (ctx javaObjectContext) arrayOperands(expression *JavaExpression) (value, index, component string, err error) {
	if expression.Target == nil || expression.Operand == nil {
		return "", "", "", fmt.Errorf("missing attributed Java array operand or index")
	}
	component = ctx.arrayElementType(expression.Target.Type)
	if component == "" {
		return "", "", "", fmt.Errorf("unsupported Java array access for %s", expression.Target.Type)
	}
	value, err = ctx.expression(expression.Target)
	if err != nil {
		return "", "", "", err
	}
	index, err = ctx.converted(expression.Operand, "int")
	return value, index, component, err
}

func (ctx javaObjectContext) arrayAccess(expression *JavaExpression) (string, error) {
	value, index, component, err := ctx.arrayOperands(expression)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("func() %s {\n\tjavaArray := %s\n\tjavaIndex := %s\n\treturn %s\n}()",
		ctx.program.objectGoType(component), value, index, ctx.arrayGet("javaArray", "javaIndex", component)), nil
}

func arrayMutationTarget(target *JavaExpression) *JavaExpression {
	for ; target != nil; target = target.Operand {
		if target.Kind == "ARRAY_ACCESS" {
			return target
		}
		if target.Kind != "PARENTHESIZED" {
			return nil
		}
	}
	return nil
}

func (ctx javaObjectContext) arrayAssignment(target, source *JavaExpression) (string, error) {
	value, index, component, err := ctx.arrayOperands(target)
	if err != nil {
		return "", err
	}
	if source == nil {
		return "", fmt.Errorf("missing attributed Java array assignment value")
	}
	element, err := ctx.converted(source, component)
	if err != nil {
		return "", err
	}
	goType := ctx.program.objectGoType(component)
	// Evaluate the simple assignment's RHS before checking null or bounds.
	return fmt.Sprintf("func() %s {\n\tjavaArray := %s\n\tjavaIndex := %s\n\tvar javaElement %s = %s\n\t%s\n\treturn javaElement\n}()",
		goType, value, index, goType, element, ctx.arraySet("javaArray", "javaIndex", component, "javaElement")), nil
}
