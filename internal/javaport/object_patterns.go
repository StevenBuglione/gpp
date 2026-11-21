package javaport

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

func (ctx javaObjectContext) patternScope() javaObjectContext {
	ctx.patterns = map[int64]string{}
	return ctx
}

func (ctx javaObjectContext) emitPatternStatements(out *strings.Builder, body *MethodBody, result string) error {
	ctx = ctx.patternScope()
	var lowered strings.Builder
	if err := ctx.emitStatements(&lowered, body, result); err != nil {
		return err
	}
	ctx.writePatternBody(out, &lowered)
	return nil
}

func javaPatternName(id int64) string {
	return fmt.Sprintf("javaPattern%d", id)
}

func (ctx javaObjectContext) writePatternBody(out, body *strings.Builder) {
	// javac enforces flow scoping and identifies each resolved use. Go
	// storage spans the invocation so a matched value can survive an if,
	// loop or short-circuit operand whose other path completes abruptly.
	for _, id := range slices.Sorted(maps.Keys(ctx.patterns)) {
		name := javaPatternName(id)
		fmt.Fprintf(out, "\tvar %s %s\n\t_ = %s\n", name, ctx.patterns[id], name)
	}
	out.WriteString(body.String())
}

func (ctx javaObjectContext) patternSlot(id int64, javaType string) (string, error) {
	if id <= 0 || ctx.patterns == nil {
		return "", fmt.Errorf("missing attributed Java pattern binding")
	}
	goType := ctx.program.objectGoType(javaType)
	if goType == "" || !ctx.program.referenceType(javaType) {
		return "", fmt.Errorf("unsupported Java reference pattern type %s", javaType)
	}
	ctx.patterns[id] = goType
	return javaPatternName(id), nil
}

func (ctx javaObjectContext) instanceOfBinding(expression *JavaExpression) (string, error) {
	if expression.Operand == nil || expression.Target == nil {
		return "", fmt.Errorf("missing attributed Java instanceof binding")
	}
	source, target := expression.Operand.Type, expression.Target.Type
	if source != "null" && !ctx.program.referenceType(source) {
		return "", fmt.Errorf("unsupported Java reference pattern operand %s", source)
	}
	slot, err := ctx.patternSlot(expression.BindingID, target)
	if err != nil {
		return "", err
	}
	value, err := ctx.expression(expression.Operand)
	if err != nil {
		return "", err
	}
	ctx.program.runtime = true
	var match string
	if ctx.arrayElementType(target) != "" {
		typeOf, err := ctx.arrayTypeCall(target, "ArrayTypeOf")
		if err != nil {
			return "", err
		}
		match = "javart.MatchArray(" + value + ", " + typeOf + ")"
	} else if iface := ctx.program.interfaces[target]; iface != nil {
		match = ctx.interfaceReferenceCall("MatchInterface", iface, value)
	} else {
		match = "javart.MatchReference[" + ctx.program.objectGoType(target) + "](" + value + ")"
	}
	// Assign only after a successful match. The single nominal check also
	// returns the proper base view for most-derived Go objects.
	return fmt.Sprintf("func() bool { javaView, javaMatched := %s; if javaMatched { %s = javaView }; return javaMatched }()",
		match, slot), nil
}
