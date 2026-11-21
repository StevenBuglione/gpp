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
	if goType == "" {
		return "", fmt.Errorf("unsupported Java pattern variable type %s", javaType)
	}
	ctx.patterns[id] = goType
	return javaPatternName(id), nil
}

func (ctx javaObjectContext) instanceOfBinding(expression *JavaExpression) (string, error) {
	if expression.Operand == nil || expression.Target == nil || expression.BindingID <= 0 {
		return "", fmt.Errorf("missing attributed Java instanceof binding")
	}
	source, target := expression.Operand.Type, expression.Target.Type
	if source != "null" && !ctx.program.referenceType(source) {
		return "", fmt.Errorf("unsupported Java reference pattern operand %s", source)
	}
	value, err := ctx.referenceTestValue(expression.Operand)
	if err != nil {
		return "", err
	}
	return ctx.typePatternTest(value, target, expression.BindingID)
}

func (ctx javaObjectContext) referencePatternMatch(value, target string) (string, error) {
	if !ctx.program.referenceType(target) {
		return "", fmt.Errorf("unsupported Java reference pattern type %s", target)
	}
	ctx.program.runtime = true
	if ctx.arrayElementType(target) != "" {
		typeOf, err := ctx.arrayTypeCall(target, "ArrayTypeOf")
		if err != nil {
			return "", err
		}
		return "javart.MatchArray(" + value + ", " + typeOf + ")", nil
	}
	if iface := ctx.program.interfaces[target]; iface != nil {
		return ctx.interfaceReferenceCall("MatchInterface", iface, value), nil
	}
	return "javart.MatchReference[" + ctx.program.objectGoType(target) + "](" + value + ")", nil
}

func (ctx javaObjectContext) typePatternTest(value, target string, bindingID int64) (string, error) {
	match, err := ctx.referencePatternMatch(value, target)
	if err != nil {
		return "", err
	}
	if bindingID == 0 {
		return "func() bool { _, javaMatched := " + match + "; return javaMatched }()", nil
	}
	slot, err := ctx.patternSlot(bindingID, target)
	if err != nil {
		return "", err
	}
	// Assign only after a successful match. The single nominal check also
	// returns the proper base view for most-derived Go objects.
	return fmt.Sprintf("func() bool { javaView, javaMatched := %s; if javaMatched { %s = javaView }; return javaMatched }()",
		match, slot), nil
}
