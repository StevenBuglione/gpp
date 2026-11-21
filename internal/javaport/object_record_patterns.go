package javaport

import (
	"fmt"
	"strings"
)

type javaRecordPatternMatch struct {
	context  javaObjectContext
	checks   strings.Builder
	bindings strings.Builder
	nextTemp int
}

func (ctx javaObjectContext) instanceOfRecordPattern(expression *JavaExpression) (string, error) {
	if expression.Operand == nil || expression.RecordPattern == nil {
		return "", fmt.Errorf("missing attributed Java instanceof record pattern")
	}
	source := expression.Operand.Type
	if source != "null" && !ctx.program.referenceType(source) {
		return "", fmt.Errorf("unsupported Java record pattern operand %s", source)
	}
	value, err := ctx.expression(expression.Operand)
	if err != nil {
		return "", err
	}
	return ctx.recordPatternTest(value, *expression.RecordPattern)
}

func (ctx javaObjectContext) recordPatternTest(value string, pattern JavaSwitchPattern) (string, error) {
	match := javaRecordPatternMatch{context: ctx}
	if err := match.record(value, pattern); err != nil {
		return "", err
	}
	return "func() bool {\n" + match.checks.String() + match.bindings.String() + "return true\n}()", nil
}

func (matcher *javaRecordPatternMatch) temp() string {
	matcher.nextTemp++
	return fmt.Sprintf("javaComponent%d", matcher.nextTemp)
}

func (matcher *javaRecordPatternMatch) record(value string, pattern JavaSwitchPattern) error {
	record, err := matcher.matchedView(value, pattern.Type, false)
	if err != nil {
		return err
	}
	return matcher.recordComponents(record, pattern)
}

func (matcher *javaRecordPatternMatch) matchedView(value, target string, nullable bool) (string, error) {
	call, err := matcher.context.referencePatternMatch(value, target)
	if err != nil {
		return "", err
	}
	view := matcher.temp()
	fmt.Fprintf(&matcher.checks, "%s, %sMatched := %s\n", view, view, call)
	miss := "!" + view + "Matched"
	if nullable {
		miss += " && javart.NormalizeReference[any](" + value + ") != nil"
	}
	fmt.Fprintf(&matcher.checks, "if %s { return false }\n", miss)
	return view, nil
}

func (matcher *javaRecordPatternMatch) recordComponents(record string, pattern JavaSwitchPattern) error {
	ctx := matcher.context
	class := ctx.program.classes[pattern.Type]
	if class == nil || class.declaration.Kind != "RECORD" {
		return fmt.Errorf("unsupported Java deconstruction pattern type %s", pattern.Type)
	}
	components := class.declaration.RecordComponents
	if len(pattern.Components) != len(components) {
		return fmt.Errorf("missing attributed component patterns for %s", pattern.Type)
	}
	if len(components) == 0 {
		fmt.Fprintf(&matcher.checks, "_ = %s\n", record)
	}
	type childRecord struct {
		value   string
		pattern JavaSwitchPattern
	}
	var children []childRecord
	for index, component := range components {
		accessor := ctx.program.methods[recordMethodSymbol(class, component.Name, nil)]
		if accessor == nil || !accessor.public {
			return fmt.Errorf("missing Java record accessor %s.%s", pattern.Type, component.Name)
		}
		extracted := matcher.temp()
		fmt.Fprintf(&matcher.checks, "%s := javart.DeconstructRecordComponent(func() %s { return %s.%s() })\n",
			extracted, ctx.program.objectGoType(component.Type), record, accessor.goName)
		child := pattern.Components[index]
		if child.Kind == "DECONSTRUCTION_PATTERN" {
			view, err := matcher.matchedView(extracted, child.Type, false)
			if err != nil {
				return err
			}
			children = append(children, childRecord{view, child})
		} else if err := matcher.component(extracted, component.Type, child); err != nil {
			return err
		}
	}
	// Check every immediate component before reading nested components.
	for _, child := range children {
		if err := matcher.recordComponents(child.value, child.pattern); err != nil {
			return err
		}
	}
	return nil
}

func (matcher *javaRecordPatternMatch) component(value, source string, pattern JavaSwitchPattern) error {
	ctx := matcher.context
	switch pattern.Kind {
	case "ANY_PATTERN":
		fmt.Fprintf(&matcher.checks, "_ = %s\n", value)
		return nil
	case "BINDING_PATTERN":
		if _, primitive := primitiveTypes[pattern.Type]; primitive {
			if source != pattern.Type {
				return fmt.Errorf("unsupported primitive record pattern conversion %s to %s", source, pattern.Type)
			}
		} else {
			view, err := matcher.matchedView(value, pattern.Type, pattern.Nullable)
			if err != nil {
				return err
			}
			value = view
		}
		if pattern.BindingID == 0 {
			fmt.Fprintf(&matcher.checks, "_ = %s\n", value)
			return nil
		}
		slot, err := ctx.patternSlot(pattern.BindingID, pattern.Type)
		if err != nil {
			return err
		}
		// No component binding is initialized until every component matches.
		fmt.Fprintf(&matcher.bindings, "%s = %s\n", slot, value)
		return nil
	default:
		return fmt.Errorf("unsupported Java record component pattern %s", pattern.Kind)
	}
}

func (ctx javaObjectContext) validateRecordPatternArms(cases []JavaSwitchCase) error {
	// javac can share component loads in consecutive record arms with
	// matching outer types and nonnull first tests. Repeating implicit
	// field reads is harmless. Reject observable custom accessors in that
	// situation until the shared-prefix evaluation is supported.
	for index := 1; index < len(cases); index++ {
		previous, current := cases[index-1], cases[index]
		if len(previous.Patterns) != 1 || len(current.Patterns) != 1 {
			continue
		}
		left, right := previous.Patterns[0], current.Patterns[0]
		if left.Kind != "DECONSTRUCTION_PATTERN" || right.Kind != "DECONSTRUCTION_PATTERN" || left.Type != right.Type ||
			len(left.Components) == 0 || len(right.Components) == 0 {
			continue
		}
		first, next := left.Components[0], right.Components[0]
		if first.Nullable || next.Nullable || first.Kind == "ANY_PATTERN" || next.Kind == "ANY_PATTERN" {
			continue
		}
		if ctx.hasCustomPatternAccessor(left) || ctx.hasCustomPatternAccessor(right) {
			return fmt.Errorf("%d:%d: unsupported shared accessors across consecutive Java record patterns for %s",
				current.Line, current.Column, right.Type)
		}
	}
	return nil
}

func (ctx javaObjectContext) hasCustomPatternAccessor(pattern JavaSwitchPattern) bool {
	class := ctx.program.classes[pattern.Type]
	if pattern.Kind != "DECONSTRUCTION_PATTERN" || class == nil {
		return false
	}
	for _, component := range class.declaration.RecordComponents {
		accessor := ctx.program.methods[recordMethodSymbol(class, component.Name, nil)]
		if accessor != nil && !accessor.recordImplicit {
			return true
		}
	}
	for _, component := range pattern.Components {
		if ctx.hasCustomPatternAccessor(component) {
			return true
		}
	}
	return false
}
