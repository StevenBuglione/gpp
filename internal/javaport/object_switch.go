package javaport

import (
	"fmt"
	"strings"
)

type javaYieldTarget struct {
	result   string
	tryScope *javaTryBranches
	recover  bool
}

func needsJavaControlFlowTerminal(kind string) bool {
	return kind == "SWITCH" || isJavaLoop(kind)
}

func (ctx javaObjectContext) switchStatement(statement JavaStatement, result, label string) (string, error) {
	ctx = ctx.localScope().pushControlTarget(label, false, false)
	ctx.control.isSwitch = true
	code, err := ctx.emitJavaSwitch(statement.Expression, statement.Cases, result, ctx.control.id, ctx.control)
	if err != nil {
		return "", err
	}
	return "{\n" + code + "}", nil
}

func (ctx javaObjectContext) switchExpression(expression *JavaExpression) (string, error) {
	goType := ctx.program.objectGoType(expression.Type)
	if goType == "" {
		return "", fmt.Errorf("unsupported Java switch result type %s", expression.Type)
	}
	ctx = ctx.localScope()
	// Java forbids break and continue from jumping out of a switch
	// expression. Nested loops and switch statements make their own targets.
	ctx.control = nil
	ctx.yield = &javaYieldTarget{result: expression.Type, tryScope: ctx.tryScope}
	(*ctx.controlID)++
	code, err := ctx.emitJavaSwitch(expression.Operand, expression.Cases, "void", *ctx.controlID, nil)
	if err != nil {
		return "", err
	}
	signature, boundary := goType, ""
	if ctx.yield.recover {
		signature = "(javaYielded " + goType + ")"
		boundary = "defer javart.RecoverReturn(&javaYielded)\n"
	}
	return fmt.Sprintf("func() %s {\n%s%spanic(\"Java switch expression completed without a value\")\n}()",
		signature, boundary, code), nil
}

func (ctx javaObjectContext) switchYield(expression *JavaExpression) (string, error) {
	if expression == nil || ctx.yield == nil {
		return "", fmt.Errorf("missing attributed Java yield value or target")
	}
	value, err := ctx.converted(expression, ctx.yield.result)
	if err != nil {
		return "", err
	}
	if ctx.tryScope == ctx.yield.tryScope {
		return "return " + value, nil
	}
	// Reuse the typed result signal across the synthetic try functions. The
	// switch's own recovery boundary consumes it before the enclosing method.
	ctx.yield.recover = true
	ctx.program.runtime = true
	return fmt.Sprintf("javart.Return[%s](%s)", ctx.program.objectGoType(ctx.yield.result), value), nil
}

func (ctx javaObjectContext) emitJavaSwitch(selector *JavaExpression, cases []JavaSwitchCase, result string, id int, target *javaControlTarget) (string, error) {
	var code, declarations, arms strings.Builder
	if err := ctx.emitJavaSwitchMatch(&code, selector, cases, target == nil); err != nil {
		return "", err
	}
	for index, arm := range cases {
		fmt.Fprintf(&arms, "case %d:\n", index)
		if err := ctx.emitJavaSwitchCase(&arms, &declarations, arm, result, id, index, target == nil); err != nil {
			return "", fmt.Errorf("%d:%d: %w", arm.Line, arm.Column, err)
		}
		if arm.Group != nil && index+1 < len(cases) {
			arms.WriteString("fallthrough\n")
		}
	}
	code.WriteString(declarations.String())
	if target != nil && target.useName {
		fmt.Fprintf(&code, "%s:\n", target.name())
	}
	fmt.Fprintf(&code, "switch javaSwitchCase {\n%s}\n", arms.String())
	return code.String(), nil
}

func (ctx javaObjectContext) emitJavaSwitchMatch(out *strings.Builder, selector *JavaExpression, cases []JavaSwitchCase, isExpression bool) error {
	if selector == nil {
		return fmt.Errorf("missing attributed Java switch selector")
	}
	if err := ctx.validateRecordPatternArms(cases); err != nil {
		return err
	}
	selectorType := selector.Type
	goType := ctx.program.objectGoType(selectorType)
	if goType == "" {
		return fmt.Errorf("unsupported Java switch selector type %s", selectorType)
	}
	primitive, reference := selectorType, ctx.program.referenceType(selectorType)
	if boxed := wrapperPrimitive(selectorType); boxed != "" {
		primitive = boxed
	}
	if !reference && !legacySwitchInteger(primitive) {
		return fmt.Errorf("unsupported Java switch selector type %s", selectorType)
	}
	value, err := ctx.expression(selector)
	if err != nil {
		return err
	}
	nullCase, defaultCase := -1, -1
	selectorClass := ctx.program.classes[selectorType]
	enumSelector := selectorClass != nil && selectorClass.isEnum()
	requiresMatch := isExpression || (reference && selectorType != javaStringType && !legacySwitchInteger(primitive) && !enumSelector)
	var matches strings.Builder
	for index, arm := range cases {
		if arm.Pattern && len(arm.Patterns) == 0 {
			return fmt.Errorf("%d:%d: unsupported Java switch pattern label", arm.Line, arm.Column)
		}
		if arm.Default {
			defaultCase = index
		}
		var labels []string
		for _, label := range arm.Labels {
			if label == nil {
				return fmt.Errorf("missing attributed Java switch case constant")
			}
			if label.Kind == "NULL_LITERAL" {
				requiresMatch = true
				nullCase = index
				continue
			}
			var match string
			if field, found := ctx.program.fields[label.Symbol]; found && field.declaration.EnumConstant {
				value, err := ctx.enumSwitchMatch(selectorType, field)
				if err != nil {
					return err
				}
				match = value
			} else if selectorType == javaStringType {
				labelValue, err := ctx.converted(label, javaStringType)
				if err != nil {
					return err
				}
				ctx.program.runtime = true
				match = ctx.checkedRuntimeCall("javart.StringEquals(javaSelector, "+labelValue+")", "boolean")
			} else if legacySwitchInteger(primitive) {
				labelValue, err := ctx.converted(label, primitive)
				if err != nil {
					return err
				}
				operand, err := ctx.convertValue("javaSelector", selectorType, primitive)
				if err != nil {
					return err
				}
				match = operand + " == " + labelValue
			} else {
				return fmt.Errorf("unsupported Java switch case for selector %s", selectorType)
			}
			labels = append(labels, match)
		}
		if len(arm.Patterns) != 0 {
			requiresMatch = true
			test, err := ctx.switchPatternTest(arm.Patterns, arm.Guard)
			if err != nil {
				return fmt.Errorf("%d:%d: %w", arm.Line, arm.Column, err)
			}
			labels = append(labels, test)
		}
		if len(labels) != 0 {
			fmt.Fprintf(&matches, "case %s:\njavaSwitchCase = %d\n", strings.Join(labels, ", "), index)
		}
	}
	fmt.Fprintf(out, "var javaSelector %s = %s\n_ = javaSelector\njavaSwitchCase := -1\nswitch {\n", goType, value)
	if reference {
		ctx.program.runtime = true
		out.WriteString("case javart.NormalizeReference[any](javaSelector) == nil:\n")
		if nullCase == -1 {
			out.WriteString("javart.Throw(nil)\n")
		} else {
			fmt.Fprintf(out, "javaSwitchCase = %d\n", nullCase)
		}
	}
	out.WriteString(matches.String())
	if defaultCase != -1 {
		fmt.Fprintf(out, "default:\njavaSwitchCase = %d\n", defaultCase)
	} else if requiresMatch {
		ctx.program.runtime = true
		out.WriteString("default:\njavart.Throw(&javart.MatchException{})\n")
	}
	out.WriteString("}\n")
	return nil
}

func (ctx javaObjectContext) switchPatternTest(patterns []JavaSwitchPattern, guard *JavaExpression) (string, error) {
	tests := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		var test string
		var err error
		switch pattern.Kind {
		case "BINDING_PATTERN":
			test, err = ctx.typePatternTest("javaSelector", pattern.Type, pattern.BindingID)
		case "DECONSTRUCTION_PATTERN":
			test, err = ctx.recordPatternTest("javaSelector", pattern)
		default:
			return "", fmt.Errorf("unsupported Java switch pattern label %s", pattern.Kind)
		}
		if err != nil {
			return "", err
		}
		tests = append(tests, test)
	}
	test := "(" + strings.Join(tests, " || ") + ")"
	if guard != nil {
		condition, err := ctx.converted(guard, "boolean")
		if err != nil {
			return "", err
		}
		// Match alternatives in order, then evaluate the guard once.
		test += " && (" + condition + ")"
	}
	return test, nil
}

func legacySwitchInteger(javaType string) bool {
	switch javaType {
	case "byte", "short", "char", "int":
		return true
	}
	return false
}

func (ctx javaObjectContext) emitJavaSwitchCase(out, declarations *strings.Builder, arm JavaSwitchCase, result string, id, armIndex int, isExpression bool) error {
	if arm.Value != nil {
		var code string
		var err error
		if isExpression {
			code, err = ctx.switchYield(arm.Value)
		} else {
			code, err = ctx.statement(JavaStatement{Kind: "EXPRESSION_STATEMENT", Expression: arm.Value}, result)
		}
		if err != nil {
			return err
		}
		fmt.Fprintln(out, code)
		return nil
	}
	if arm.Rule != nil {
		code, err := ctx.statement(*arm.Rule, result)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, code)
		return nil
	}
	if arm.Group == nil {
		return fmt.Errorf("missing attributed Java switch case body")
	}
	for index, statement := range arm.Group.Code {
		if statement.Kind == "VARIABLE" && statement.Variable != nil {
			name := fmt.Sprintf("javaSwitch%dLocal%d_%d", id, armIndex, index)
			if err := ctx.declareJavaSwitchVariable(out, declarations, *statement.Variable, name); err != nil {
				return err
			}
			continue
		}
		code, err := ctx.statement(statement, result)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, code)
	}
	return nil
}

func (ctx javaObjectContext) declareJavaSwitchVariable(out, declarations *strings.Builder, variable JavaVariable, name string) error {
	goType := ctx.program.objectGoType(variable.Type)
	if goType == "" {
		return fmt.Errorf("unsupported Java switch local type %s", variable.Type)
	}
	// Colon-form groups share one Java scope. Hoist storage outside the Go
	// cases, while evaluating initializers only on paths through the group.
	fmt.Fprintf(declarations, "var %s %s\n_ = %s\n", name, goType, name)
	if variable.Initializer != nil {
		value, err := ctx.converted(variable.Initializer, variable.Type)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s = %s\n", name, value)
	}
	if variable.Name != "" && variable.Name != "_" {
		ctx.locals[variable.Name] = name
	}
	return nil
}
