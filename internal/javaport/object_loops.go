package javaport

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

type javaControlTarget struct {
	parent   *javaControlTarget
	tryScope *javaTryBranches
	id       int
	label    string
	isLoop   bool
	hasStep  bool
	useName  bool
	useStep  bool
}

// Only a try boundary that restores a branch to its lexical scope needs a
// dispatcher. Intermediate regions let the signal unwind through cleanup.
type javaTryBranches struct {
	parent   *javaTryBranches
	dispatch map[int]string
}

func (region *javaTryBranches) wrap(code string) string {
	if len(region.dispatch) == 0 {
		return code
	}
	var body strings.Builder
	fmt.Fprintf(&body, "{\njavaDestination := javart.RecoverBranch(func() {\n%s\n})\n", code)
	for _, id := range slices.Sorted(maps.Keys(region.dispatch)) {
		fmt.Fprintf(&body, "if javaDestination == %d { %s }\n", id, region.dispatch[id])
	}
	body.WriteString("if javaDestination != 0 { javart.Branch(javaDestination) }\n}")
	return body.String()
}

func isJavaLoop(kind string) bool {
	switch kind {
	case "WHILE_LOOP", "DO_WHILE_LOOP", "FOR_LOOP", "ENHANCED_FOR_LOOP":
		return true
	}
	return false
}

func (ctx javaObjectContext) pushControlTarget(label string, isLoop, hasStep bool) javaObjectContext {
	(*ctx.controlID)++
	ctx.control = &javaControlTarget{
		parent: ctx.control, tryScope: ctx.tryScope, id: *ctx.controlID,
		label: label, isLoop: isLoop, hasStep: hasStep,
	}
	return ctx
}

func (target *javaControlTarget) name() string {
	return fmt.Sprintf("javaControl%d", target.id)
}

func (target *javaControlTarget) directBranch(continues bool) string {
	if continues && target.hasStep {
		target.useStep = true
		return "goto " + target.name() + "Step"
	}
	target.useName = true
	if continues {
		return "continue " + target.name()
	}
	if target.isLoop {
		return "break " + target.name()
	}
	return "goto " + target.name()
}

func (ctx javaObjectContext) loopBranch(statement JavaStatement) (string, error) {
	continues := statement.Kind == "CONTINUE"
	for target := ctx.control; target != nil; target = target.parent {
		if statement.Label == "" && !target.isLoop || statement.Label != "" && statement.Label != target.label {
			continue
		}
		if continues && !target.isLoop {
			break
		}
		code := target.directBranch(continues)
		if ctx.tryScope == target.tryScope {
			return code, nil
		}
		// Register this branch in the outermost try crossed by the
		// branch. At its dispatch site, the Go branch target is in scope.
		for region := ctx.tryScope; region != nil; region = region.parent {
			if region.parent != target.tryScope {
				continue
			}
			id := target.id * 2
			if continues {
				id++
			}
			if region.dispatch == nil {
				region.dispatch = map[int]string{}
			}
			region.dispatch[id] = code
			return fmt.Sprintf("javart.Branch(%d)", id), nil
		}
		return "", fmt.Errorf("missing Java exception region for %s", statement.Kind)
	}
	return "", fmt.Errorf("invalid Java %s target %q", statement.Kind, statement.Label)
}

func (ctx javaObjectContext) labeledStatement(statement JavaStatement, result string) (string, error) {
	if statement.Nested == nil || statement.Label == "" {
		return "", fmt.Errorf("missing attributed Java labeled statement")
	}
	if isJavaLoop(statement.Nested.Kind) {
		return ctx.loopStatement(*statement.Nested, result, statement.Label)
	}
	ctx = ctx.pushControlTarget(statement.Label, false, false)
	body, err := ctx.statement(*statement.Nested, result)
	if err != nil {
		return "", err
	}
	if !ctx.control.useName {
		return body, nil
	}
	// A Java break may leave any labeled statement. The artificial label
	// follows a nested block so the Go goto cannot skip a local declaration.
	return "{\n{\n" + body + "\n}\n" + ctx.control.name() + ":\n_ = 0\n}", nil
}

func (ctx javaObjectContext) loopStatement(statement JavaStatement, result, label string) (string, error) {
	if statement.Nested == nil {
		return "", fmt.Errorf("missing attributed Java loop body")
	}
	if statement.Kind == "ENHANCED_FOR_LOOP" {
		return ctx.enhancedArrayFor(statement, result, label)
	}
	if statement.Kind != "FOR_LOOP" && statement.Expression == nil {
		return "", fmt.Errorf("missing attributed Java loop condition")
	}
	// Java's for initializer scope includes the condition, body and updates.
	ctx = ctx.localScope()
	var code, step strings.Builder
	code.WriteString("{\n")
	if statement.LoopInit != nil {
		if err := ctx.emitStatementsInScope(&code, statement.LoopInit, result); err != nil {
			return "", err
		}
	}
	condition := ""
	if statement.Expression != nil {
		value, err := ctx.converted(statement.Expression, "boolean")
		if err != nil {
			return "", err
		}
		if statement.Kind == "DO_WHILE_LOOP" {
			fmt.Fprintf(&step, "if !(%s) { break }\n", value)
		} else {
			condition = " " + value
		}
	}
	if statement.LoopUpdate != nil {
		if err := ctx.emitStatements(&step, statement.LoopUpdate, result); err != nil {
			return "", err
		}
	}
	ctx = ctx.pushControlTarget(label, true, step.Len() != 0)
	body, err := ctx.statement(*statement.Nested, result)
	if err != nil {
		return "", err
	}
	return finishJavaLoop(&code, ctx.control, condition, body, step.String()), nil
}

func (ctx javaObjectContext) enhancedArrayFor(statement JavaStatement, result, label string) (string, error) {
	if statement.Expression == nil || statement.Variable == nil {
		return "", fmt.Errorf("missing attributed Java enhanced-for expression or variable")
	}
	arrayType := statement.Expression.Type
	component := ctx.arrayElementType(arrayType)
	if component == "" {
		return "", fmt.Errorf("unsupported Java enhanced-for expression type %s", arrayType)
	}
	value, err := ctx.expression(statement.Expression)
	if err != nil {
		return "", err
	}
	variable := statement.Variable
	goType := ctx.program.objectGoType(variable.Type)
	if goType == "" {
		return "", fmt.Errorf("unsupported Java enhanced-for variable type %s", variable.Type)
	}
	element := ctx.arrayGet("javaArray", "javaIndex", component)
	element, err = ctx.convertValue(element, component, variable.Type)
	if err != nil {
		return "", err
	}
	ctx = ctx.localScope().pushControlTarget(label, true, true)
	name := ctx.control.name() + "Element"
	if variable.Name != "_" {
		ctx.locals[variable.Name] = name
	}
	body, err := ctx.statement(*statement.Nested, result)
	if err != nil {
		return "", err
	}
	var code strings.Builder
	// Capture the original array and check it even when the body never
	// runs. Each iteration then reads the current value of its element.
	fmt.Fprintf(&code, "{\njavaArray := %s\njavaLength := %s\njavaIndex := int32(0)\n",
		value, ctx.arrayLength("javaArray"))
	body = fmt.Sprintf("var %s %s = %s\n_ = %s\n%s", name, goType, element, name, body)
	return finishJavaLoop(&code, ctx.control, " javaIndex < javaLength", body, "javaIndex++\n"), nil
}

func finishJavaLoop(code *strings.Builder, target *javaControlTarget, condition, body, step string) string {
	if target.useName {
		fmt.Fprintf(code, "%s:\n", target.name())
	}
	fmt.Fprintf(code, "for%s {\n{\n%s\n}\n", condition, body)
	if target.useStep {
		fmt.Fprintf(code, "%sStep:\n", target.name())
	}
	code.WriteString(step)
	code.WriteString("}\n}")
	return code.String()
}
