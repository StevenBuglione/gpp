package compiler

import (
	"fmt"
	"strings"
)

// A void initializer uses this output when an inline constructor invocation
// returns a Go error. Its own exceptions still follow the ordinary GPP rules.
const initializerFailureOutput = "__gppInitFailureOut"

func initializerCallContext(method Method, call *CallExpr, context constructorContext) constructorContext {
	context.CurrentParameterTypes = parameterTypeMapFromNodes(method.ParameterAST)
	if method.BodyAST != nil {
		for _, statement := range method.BodyAST.Statements {
			if expression, ok := statement.(*ExpressionStmt); ok && expression.Expression == call {
				break
			}
			switch statement := statement.(type) {
			case *DeclarationStmt:
				for index, name := range statement.Names {
					declareInitializerLocal(name.Text, statement.Type, index, statement.Values, context)
				}
			case *AssignmentStmt:
				if statement.Operator != ":=" {
					continue
				}
				for index, expression := range statement.Left {
					if name, ok := expression.(*NameExpr); ok {
						declareInitializerLocal(name.Name, nil, index, statement.Right, context)
					}
				}
			}
		}
	}
	return context
}

func declareInitializerLocal(name string, declared TypeNode, index int, values []ExprNode, context constructorContext) {
	if name == "_" || context.CurrentParameterTypes[name] != "" {
		return
	}
	if declared != nil {
		context.CurrentParameterTypes[name], _ = typeNodeSource(declared)
	} else if index < len(values) {
		context.CurrentParameterTypes[name] = coalesceExpressionTypeNode(values[index], context, context.CurrentParameterTypes)
	}
}

func initializerArguments(arguments []string, context constructorContext) ([]CallArg, error) {
	result := make([]CallArg, 0, len(arguments))
	for _, source := range arguments {
		argument, err := parseStaticArgumentNode(source)
		if err != nil {
			return nil, err
		}
		argument, err = lowerExceptionExprNode(argument, context)
		if err != nil {
			return nil, err
		}
		result = append(result, CallArg{Value: argument})
	}
	return result, nil
}

func lowerExplicitInitializerStatement(expression ExprNode, context constructorContext) (Stmt, bool, error) {
	call, ok := expression.(*CallExpr)
	if !ok || context.CurrentMethod != "init" {
		return nil, false, nil
	}
	if parents := context.InlineParentInitializers; isSuperInitializerCall(call) && len(parents) != 0 {
		body := &BlockStmt{SpanValue: call.Span()}
		for _, parent := range parents {
			if parent.helper == "" {
				continue
			}
			receiver := ExprNode(&NameExpr{Name: "this"})
			for _, part := range parent.path {
				receiver = &SelectorExpr{Receiver: receiver, Name: classParentFieldName(part)}
			}
			arguments, err := initializerArguments(parent.arguments, context)
			if err != nil {
				return nil, true, err
			}
			invocation := &CallExpr{
				Callee:    &SelectorExpr{Receiver: receiver, Name: parent.helper},
				Arguments: arguments,
				SpanValue: call.Span(),
			}
			if parent.noError {
				body.Statements = append(body.Statements, &ExpressionStmt{Expression: invocation})
			} else {
				body.Statements = append(body.Statements, forwardInitializerFailure(invocation, context))
			}
		}
		if context.RunInstanceInitializers {
			body.Statements = append(body.Statements, instanceInitializerCall(context))
		}
		return body, true, nil
	}
	return lowerThisInitializerStatement(expression, context)
}

func forwardInitializerFailure(call *CallExpr, context constructorContext) Stmt {
	result, _ := typeNodeSource(context.CurrentResultAST)
	if strings.TrimSpace(result) != "error" && !context.InitializerFailureOutput {
		return &ExpressionStmt{
			Expression: &CallExpr{Callee: &NameExpr{Name: "__gppThrow"}, Arguments: []CallArg{{Value: call}}},
			SpanValue:  call.Span(),
		}
	}
	failure := &NameExpr{Name: "__gppInitInvocationError"}
	exit := &ReturnStmt{Values: []ExprNode{failure}}
	blocks := []Stmt{exit}
	if context.InitializerFailureOutput {
		exit.Values = nil
		save := &AssignmentStmt{
			Left:     []ExprNode{&UnaryExpr{Operator: "*", Operand: &NameExpr{Name: initializerFailureOutput}}},
			Operator: "=",
			Right:    []ExprNode{failure},
		}
		blocks = []Stmt{save, exit}
	}
	return &IfStmt{
		Init:      &AssignmentExpr{Left: []ExprNode{failure}, Operator: ":=", Right: []ExprNode{call}},
		Condition: &BinaryExpr{Left: failure, Operator: "!=", Right: &NameExpr{Name: "nil"}},
		Body:      &BlockStmt{Statements: blocks},
		SpanValue: call.Span(),
	}
}

func validateExplicitInitializerCalls(class *ClassDecl, method *Method, classes map[string]*ClassDecl) error {
	if method.BodyAST == nil {
		return nil
	}
	directCalls := map[*CallExpr]bool{}
	first := -1
	var delegated, parentCalled bool
	for index, statement := range method.BodyAST.Statements {
		expression, ok := statement.(*ExpressionStmt)
		if !ok {
			continue
		}
		call, ok := expression.Expression.(*CallExpr)
		if !ok || (!isThisInitializerCall(call) && !isSuperInitializerCall(call)) {
			continue
		}
		directCalls[call] = true
		if first < 0 {
			first = index
		}
		if isThisInitializerCall(call) {
			if delegated {
				return methodDiagnosticf(class, method, "initializer delegation this(...) may appear only once")
			}
			delegated = true
		} else {
			parentCalled = true
		}
	}
	if delegated && parentCalled {
		return methodDiagnosticf(class, method, "an initializer cannot use both this(...) and super(...)")
	}
	var invalid *CallExpr
	walkIntrospectionBlockExpressions(method.BodyAST, func(expression ExprNode) {
		if call, ok := expression.(*CallExpr); ok && (isThisInitializerCall(call) || isSuperInitializerCall(call)) && !directCalls[call] {
			if invalid == nil {
				invalid = call
			}
		}
	})
	if invalid != nil {
		return methodDiagnosticf(class, method, "a constructor invocation must be a top-level initializer statement")
	}
	if first < 0 {
		return nil
	}
	return validateInitializerPrologue(class, method, classes, first, directCalls)
}

func isThisInitializerCall(call *CallExpr) bool {
	name, ok := call.Callee.(*NameExpr)
	return ok && name.Name == "this"
}

func validateInitializerPrologue(class *ClassDecl, method *Method, classes map[string]*ClassDecl, first int, calls map[*CallExpr]bool) error {
	ownFields := map[string]bool{}
	for _, field := range class.Fields {
		ownFields[field.Name] = true
	}
	visibleFields := initializerVisibleFields(class, classes, map[*ClassDecl]bool{})
	locals := initializerLocalNames(method)
	allowed := map[*NameExpr]bool{}
	prologue := &BlockStmt{Statements: method.BodyAST.Statements[:first]}
	var earlyReturn *ReturnStmt
	walkInitializerStatements(prologue, func(statement Stmt) {
		if returned, ok := statement.(*ReturnStmt); ok && earlyReturn == nil {
			earlyReturn = returned
		}
		assignment, ok := statement.(*AssignmentStmt)
		if !ok || assignment.Operator != "=" {
			return
		}
		for _, destination := range assignment.Left {
			if name, ok := destination.(*NameExpr); ok && ownFields[name.Name] && !locals[name.Name] {
				allowed[name] = true
			}
			field, ok := destination.(*SelectorExpr)
			if !ok || !ownFields[field.Name] {
				continue
			}
			if receiver, ok := field.Receiver.(*NameExpr); ok && receiver.Name == "this" {
				allowed[receiver] = true
			}
		}
	})
	if earlyReturn != nil {
		return sourceLineError(earlyReturn.Span(), fmt.Errorf("return cannot precede a constructor invocation"))
	}
	check := func(expression ExprNode) error {
		var invalid *NameExpr
		literalKeys := map[*NameExpr]bool{}
		walkIntrospectionExpression(expression, func(node ExprNode) {
			switch node := node.(type) {
			case *CompositeLiteralExpr:
				for _, element := range node.Elements {
					if key, ok := element.Key.(*NameExpr); ok {
						literalKeys[key] = true
					}
				}
			case *NameExpr:
				if invalid == nil && !allowed[node] && !literalKeys[node] &&
					(node.Name == "this" || node.Name == "super" || (visibleFields[node.Name] && !locals[node.Name])) {
					invalid = node
				}
			}
		})
		if invalid != nil {
			return sourceLineError(invalid.Span(), fmt.Errorf("early constructor code can use this only to assign a field declared by %s", class.Name))
		}
		return nil
	}
	var problem error
	walkBlockExpressions(prologue, func(expression ExprNode) {
		if problem == nil {
			problem = check(expression)
		}
	})
	if problem != nil {
		return problem
	}
	for _, statement := range method.BodyAST.Statements[first:] {
		if expression, ok := statement.(*ExpressionStmt); ok {
			if call, ok := expression.Expression.(*CallExpr); ok && calls[call] {
				for _, argument := range call.Arguments {
					if err := check(argument.Value); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func initializerVisibleFields(class *ClassDecl, classes map[string]*ClassDecl, seen map[*ClassDecl]bool) map[string]bool {
	fields := map[string]bool{}
	if class == nil || seen[class] {
		return fields
	}
	seen[class] = true
	for _, parent := range classParentNames(class) {
		for name := range initializerVisibleFields(classes[parent], classes, seen) {
			if memberVisibleToSubclass(name) {
				fields[name] = true
			}
		}
	}
	for _, field := range class.Fields {
		fields[field.Name] = true
	}
	return fields
}

func initializerLocalNames(method *Method) map[string]bool {
	locals := map[string]bool{}
	for _, parameter := range method.ParameterAST {
		locals[parameter.Name] = true
	}
	walkInitializerStatements(method.BodyAST, func(statement Stmt) {
		switch statement := statement.(type) {
		case *DeclarationStmt:
			for _, name := range statement.Names {
				locals[name.Text] = true
			}
		case *AssignmentStmt:
			if statement.Operator == ":=" {
				for _, target := range statement.Left {
					if name, ok := target.(*NameExpr); ok {
						locals[name.Name] = true
					}
				}
			}
		}
	})
	return locals
}

func walkInitializerStatements(statement Stmt, visit func(Stmt)) {
	if statement == nil || isNilStmt(statement) {
		return
	}
	visit(statement)
	walk := func(child Stmt) { walkInitializerStatements(child, visit) }
	switch value := statement.(type) {
	case *BlockStmt:
		for _, child := range value.Statements {
			walk(child)
		}
	case *TokenStmt:
		walk(value.Body)
		for _, child := range value.Children {
			walk(child)
		}
	case *IfStmt:
		walk(value.Body)
		walk(value.Else)
		walk(value.ElseIf)
	case *ForStmt:
		walk(value.Body)
	case *SwitchStmt:
		walk(value.Body)
	case *CaseStmt:
		walk(value.Clause.Body)
	case *TryStmt:
		walk(value.Body)
		for _, clause := range value.Catches {
			walk(clause.Body)
		}
		walk(value.Finally)
	}
}
