package compiler

import (
	"fmt"
	"maps"
)

type finalAssignment uint8

const (
	finalUnassigned finalAssignment = 1 << iota
	finalAssigned
)

// A nil state is unreachable. Joining paths retains both possible states for
// each field, so a later read or write must satisfy the rule on every path.
type finalState map[string]finalAssignment

func joinFinalStates(states ...finalState) finalState {
	var merged finalState
	for _, state := range states {
		if state == nil {
			continue
		}
		if merged == nil {
			merged = maps.Clone(state)
			continue
		}
		for name, status := range state {
			merged[name] |= status
		}
	}
	return merged
}

type finalFlow struct {
	normal    finalState
	breaks    finalState
	continues finalState
	falls     finalState
	throws    finalState
}

func (flow *finalFlow) join(other finalFlow) {
	flow.normal = joinFinalStates(flow.normal, other.normal)
	flow.breaks = joinFinalStates(flow.breaks, other.breaks)
	flow.continues = joinFinalStates(flow.continues, other.continues)
	flow.falls = joinFinalStates(flow.falls, other.falls)
	flow.throws = joinFinalStates(flow.throws, other.throws)
}

type staticFinalAnalyzer struct {
	class  *ClassDecl
	blanks map[string]*Field
	err    error
}

func validateStaticFinalFields(class *ClassDecl) error {
	analyzer := &staticFinalAnalyzer{class: class, blanks: map[string]*Field{}}
	state := finalState{}
	for index := range class.StaticFields {
		field := &class.StaticFields[index]
		if field.IsFinal && field.InitializerAST == nil {
			analyzer.blanks[field.Name] = field
			state[field.Name] = finalUnassigned
		}
	}
	if len(state) == 0 {
		return nil
	}
	for _, item := range staticInitializerOrder(class) {
		if item.field != nil {
			analyzer.visitor(state).expression(item.field.InitializerAST, newFieldLocalScope(nil), false)
		} else {
			state = analyzer.block(item.block, nil, state).normal
		}
		if analyzer.err != nil {
			return analyzer.err
		}
	}
	for index := range class.StaticFields {
		field := &class.StaticFields[index]
		if analyzer.blanks[field.Name] != nil && state != nil && state[field.Name] != finalAssigned {
			return fieldDiagnosticf(class, field, "static final field %s might not be initialized", field.Name)
		}
	}
	return nil
}

func (analyzer *staticFinalAnalyzer) problem(span Span, format string, arguments ...any) {
	if analyzer.err == nil {
		analyzer.err = diagnosticAt(analyzer.class.SourceFile, span, fmt.Errorf(format, arguments...))
	}
}

func (analyzer *staticFinalAnalyzer) visitor(state finalState) classFieldVisitor {
	return func(use classFieldUse) {
		field := analyzer.blanks[use.name.Name]
		if analyzer.err != nil || state == nil || field == nil || (use.callee && !isFunctionType(field.TypeAST)) {
			return
		}
		name := field.Name
		if use.qualifier != "" {
			className := analyzer.class.Name
			if use.qualifier == className || use.qualifier == staticFieldScopePrefix+className || use.qualifier == staticFinalInitializerPrefix+className {
				analyzer.problem(use.name.Span(), "cannot assign to static final field %s.%s using a qualified name", className, name)
			}
			return
		}
		if use.write {
			switch {
			case use.closure:
				analyzer.problem(use.name.Span(), "cannot assign static final field %s in a function literal", name)
			case state[name] != finalUnassigned:
				analyzer.problem(use.name.Span(), "static final field %s might already be assigned", name)
			default:
				state[name] = finalAssigned
			}
		} else if state[name] != finalAssigned {
			analyzer.problem(use.name.Span(), "static final field %s might not be initialized", name)
		}
	}
}

func (analyzer *staticFinalAnalyzer) block(block *BlockStmt, parent *fieldLocalScope, state finalState) finalFlow {
	flow := finalFlow{normal: maps.Clone(state)}
	if block == nil || state == nil {
		return flow
	}
	scope := newFieldLocalScope(parent)
	for _, statement := range block.Statements {
		next := analyzer.statement(statement, scope, flow.normal)
		flow.normal = nil
		flow.join(next)
		if flow.normal == nil || analyzer.err != nil {
			break
		}
	}
	return flow
}

func (analyzer *staticFinalAnalyzer) statement(statement Stmt, scope *fieldLocalScope, state finalState) finalFlow {
	if statement == nil || isNilStmt(statement) || state == nil || analyzer.err != nil {
		return finalFlow{normal: state}
	}
	switch node := statement.(type) {
	case *BlockStmt:
		return analyzer.block(node, scope, state)
	case *IfStmt:
		nested := newFieldLocalScope(scope)
		analyzer.visitor(state).expression(node.Init, nested, false)
		onTrue, onFalse := analyzer.condition(node.Condition, nested, state)
		then := analyzer.block(node.Body, nested, onTrue)
		if node.ElseIf != nil {
			then.join(analyzer.statement(node.ElseIf, nested, onFalse))
		} else {
			then.join(analyzer.block(node.Else, nested, onFalse))
		}
		return then
	case *ForStmt:
		return analyzer.loop(node, scope, state)
	case *SwitchStmt:
		return analyzer.switchFlow(node, scope, state)
	case *TryStmt:
		return analyzer.tryFlow(node, scope, state)
	case *BranchStmt:
		if len(node.Target) != 0 || node.Keyword == "goto" {
			analyzer.problem(node.Span(), "labeled branches in static final initialization are not supported")
		}
		switch node.Keyword {
		case "break":
			return finalFlow{breaks: state}
		case "continue":
			return finalFlow{continues: state}
		case "fallthrough":
			return finalFlow{falls: state}
		}
	case *ThrowStmt:
		analyzer.visitor(state).expression(node.Value, scope, false)
		return finalFlow{throws: state}
	case *TokenStmt:
		if node.Body != nil || len(node.Children) != 0 {
			analyzer.problem(node.Span(), "cannot analyze static final initialization in this statement")
			return finalFlow{}
		}
	}
	analyzer.visitor(state).statement(statement, scope)
	return finalFlow{normal: state}
}

func (analyzer *staticFinalAnalyzer) condition(expression ExprNode, scope *fieldLocalScope, state finalState) (finalState, finalState) {
	if state == nil {
		return nil, nil
	}
	switch node := expression.(type) {
	case *ParenthesizedExpr:
		return analyzer.condition(node.Inner, scope, state)
	case *UnaryExpr:
		if node.Operator == "!" {
			onTrue, onFalse := analyzer.condition(node.Operand, scope, state)
			return onFalse, onTrue
		}
	case *BinaryExpr:
		switch node.Operator {
		case "&&":
			onTrue, onFalse := analyzer.condition(node.Left, scope, state)
			rightTrue, rightFalse := analyzer.condition(node.Right, scope, onTrue)
			return rightTrue, joinFinalStates(onFalse, rightFalse)
		case "||":
			onTrue, onFalse := analyzer.condition(node.Left, scope, state)
			rightTrue, rightFalse := analyzer.condition(node.Right, scope, onFalse)
			return joinFinalStates(onTrue, rightTrue), rightFalse
		}
	case *LiteralExpr:
		switch node.Text {
		case "true":
			return state, nil
		case "false":
			return nil, state
		}
	}
	state = maps.Clone(state)
	analyzer.visitor(state).expression(expression, scope, false)
	return state, maps.Clone(state)
}

func (analyzer *staticFinalAnalyzer) loop(node *ForStmt, scope *fieldLocalScope, state finalState) finalFlow {
	nested := newFieldLocalScope(scope)
	analyzer.visitor(state).expression(node.Init, nested, false)
	analyzer.visitor(state).expression(node.RangeExpr, nested, false)
	initial := maps.Clone(state)
	header := initial
	for {
		onTrue, onFalse := analyzer.condition(node.Condition, nested, header)
		if node.Condition == nil && node.RangeExpr == nil {
			onFalse = nil
		}
		if onTrue != nil {
			analyzer.visitor(onTrue).assignment(node.RangeKey, nil, node.RangeOperator, nested)
		}
		body := analyzer.block(node.Body, nested, onTrue)
		repeat := joinFinalStates(body.normal, body.continues)
		analyzer.visitor(repeat).expression(node.Post, nested, false)
		next := joinFinalStates(initial, repeat)
		if analyzer.err != nil || maps.Equal(header, next) {
			return finalFlow{normal: joinFinalStates(onFalse, body.breaks), throws: body.throws}
		}
		header = next
	}
}

func (analyzer *staticFinalAnalyzer) switchFlow(node *SwitchStmt, scope *fieldLocalScope, state finalState) finalFlow {
	nested := newFieldLocalScope(scope)
	analyzer.visitor(state).expression(node.Init, nested, false)
	analyzer.visitor(state).expression(node.Tag, nested, false)
	skipCaseTypes := isTypeSwitchAssertion(node.Tag) || isTypeSwitchAssignment(node.Init)
	var exits finalFlow
	defaultCase := false
	if node.Body == nil {
		return finalFlow{normal: state}
	}
	for _, statement := range node.Body.Statements {
		branch, ok := statement.(*CaseStmt)
		if !ok {
			analyzer.problem(statement.Span(), "cannot analyze static final initialization in this switch")
			break
		}
		defaultCase = defaultCase || branch.Clause.Default
		branchScope := newFieldLocalScope(nested)
		input := joinFinalStates(state, exits.falls)
		exits.falls = nil
		if !skipCaseTypes {
			for _, expression := range branch.Clause.Expressions {
				analyzer.visitor(input).expression(expression, branchScope, false)
			}
		}
		exits.join(analyzer.block(branch.Clause.Body, branchScope, input))
	}
	if !defaultCase && !node.Select {
		exits.normal = joinFinalStates(exits.normal, state)
	}
	exits.normal = joinFinalStates(exits.normal, exits.breaks)
	exits.breaks = nil
	return exits
}

func (analyzer *staticFinalAnalyzer) tryFlow(node *TryStmt, scope *fieldLocalScope, state finalState) finalFlow {
	body := analyzer.block(node.Body, scope, state)
	// A catch or finally starts with the assignments known before the try,
	// and must also allow for any assignments before an abrupt exit.
	catchState := joinFinalStates(state, body.normal, body.throws, body.breaks, body.continues, body.falls)
	for _, clause := range node.Catches {
		nested := newFieldLocalScope(scope)
		nested.names[clause.Binding] = true
		body.join(analyzer.block(clause.Body, nested, catchState))
	}
	if node.Finally == nil {
		return body
	}
	cleanupState := joinFinalStates(catchState, body.normal, body.throws, body.breaks, body.continues, body.falls)
	cleanup := analyzer.block(node.Finally, scope, cleanupState)
	if cleanup.normal == nil {
		return cleanup
	}
	apply := func(exit finalState) finalState {
		if exit == nil {
			return nil
		}
		result := maps.Clone(exit)
		for name, status := range cleanup.normal {
			if status != cleanupState[name] {
				result[name] = status
			}
		}
		return result
	}
	body.normal = apply(body.normal)
	body.breaks = apply(body.breaks)
	body.continues = apply(body.continues)
	body.falls = apply(body.falls)
	body.throws = apply(body.throws)
	cleanup.normal = nil
	body.join(cleanup)
	return body
}
