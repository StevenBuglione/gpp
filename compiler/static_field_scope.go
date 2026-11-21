package compiler

import (
	"fmt"
	"sort"
	"strings"
)

// Keep a bound field distinct from an explicitly qualified source reference.
// A local identifier with the class's name cannot shadow the lexical owner.
const staticFieldScopePrefix = "__gppStaticScope_"

type fieldLocalScope struct {
	parent            *fieldLocalScope
	names             map[string]bool
	closure           bool
	qualifiedSelector func(string, *SelectorExpr)
}

func newFieldLocalScope(parent *fieldLocalScope) *fieldLocalScope {
	scope := &fieldLocalScope{parent: parent, names: map[string]bool{}}
	if parent != nil {
		scope.closure = parent.closure
		scope.qualifiedSelector = parent.qualifiedSelector
	}
	return scope
}

func (scope *fieldLocalScope) contains(name string) bool {
	for current := scope; current != nil; current = current.parent {
		if current.names[name] {
			return true
		}
	}
	return false
}

type classFieldUse struct {
	name      *NameExpr
	qualifier string
	write     bool // bare destination of an ordinary assignment
	callee    bool
	closure   bool
}

type classFieldVisitor func(classFieldUse)

func (visit classFieldVisitor) block(block *BlockStmt, enclosing *fieldLocalScope) {
	if block == nil {
		return
	}
	scope := newFieldLocalScope(enclosing)
	for _, stmt := range block.Statements {
		visit.statement(stmt, scope)
	}
}

func (visit classFieldVisitor) statement(stmt Stmt, scope *fieldLocalScope) {
	if stmt == nil || isNilStmt(stmt) {
		return
	}
	expr := func(node ExprNode) { visit.expression(node, scope, false) }
	switch node := stmt.(type) {
	case *BlockStmt:
		visit.block(node, scope)
	case *DeclarationStmt:
		for _, value := range node.Values {
			expr(value)
		}
		for _, name := range node.Names {
			scope.names[name.Text] = true
		}
	case *TypeDeclarationStmt:
		scope.names[node.Name] = true
	case *AssignmentStmt:
		visit.assignment(node.Left, node.Right, node.Operator, scope)
	case *IfStmt:
		nested := newFieldLocalScope(scope)
		visit.expression(node.Init, nested, false)
		visit.expression(node.Condition, nested, false)
		visit.block(node.Body, nested)
		visit.block(node.Else, nested)
		visit.statement(node.ElseIf, nested)
	case *ForStmt:
		nested := newFieldLocalScope(scope)
		visit.expression(node.Init, nested, false)
		visit.expression(node.Condition, nested, false)
		visit.expression(node.Post, nested, false)
		visit.expression(node.RangeExpr, nested, false)
		visit.assignment(node.RangeKey, nil, node.RangeOperator, nested)
		visit.block(node.Body, nested)
	case *SwitchStmt:
		nested := newFieldLocalScope(scope)
		visit.expression(node.Init, nested, false)
		visit.expression(node.Tag, nested, false)
		typeSwitch := isTypeSwitchAssertion(node.Tag) || isTypeSwitchAssignment(node.Init)
		if node.Body != nil && typeSwitch {
			for _, statement := range node.Body.Statements {
				if branch, ok := statement.(*CaseStmt); ok {
					visit.block(branch.Clause.Body, nested)
				} else {
					visit.statement(statement, nested)
				}
			}
		} else {
			visit.block(node.Body, nested)
		}
	case *CaseStmt:
		nested := newFieldLocalScope(scope)
		for _, value := range node.Clause.Expressions {
			visit.expression(value, nested, false)
		}
		visit.block(node.Clause.Body, nested)
	case *TryStmt:
		visit.block(node.Body, scope)
		for _, clause := range node.Catches {
			nested := newFieldLocalScope(scope)
			nested.names[clause.Binding] = true
			visit.block(clause.Body, nested)
		}
		visit.block(node.Finally, scope)
	case *TokenStmt:
		for _, value := range node.Exprs {
			expr(value)
		}
		visit.block(node.Body, scope)
		for _, child := range node.Children {
			visit.statement(child, scope)
		}
	default:
		// Terminal statements have no child scopes or local declarations.
		walkStmtExpressions(stmt, expr)
	}
}

func (visit classFieldVisitor) assignment(left, right []ExprNode, operator string, scope *fieldLocalScope) {
	for _, operand := range right {
		visit.expression(operand, scope, false)
	}
	for _, operand := range left {
		if name, ok := operand.(*NameExpr); operator == ":=" && ok {
			scope.names[name.Name] = true
		} else {
			visit.expression(operand, scope, operator == "=")
		}
	}
}

func (visit classFieldVisitor) expression(expr ExprNode, scope *fieldLocalScope, write bool) {
	child := func(value ExprNode) { visit.expression(value, scope, false) }
	switch node := expr.(type) {
	case *NameExpr:
		if node.Name != "_" && !scope.contains(node.Name) {
			visit(classFieldUse{name: node, write: write, closure: scope.closure})
		}
	case *UnaryExpr:
		child(node.Operand)
	case *BinaryExpr:
		child(node.Left)
		child(node.Right)
	case *AssignmentExpr:
		visit.assignment(node.Left, node.Right, node.Operator, scope)
	case *SelectorExpr:
		if scope.qualifiedSelector != nil {
			if qualifier := scopedClassQualifier(node.Receiver, scope); qualifier != "" {
				scope.qualifiedSelector(qualifier, node)
			}
		}
		if write {
			if receiver, ok := node.Receiver.(*NameExpr); ok && !scope.contains(receiver.Name) {
				visit(classFieldUse{name: &NameExpr{Name: node.Name, SpanValue: node.Span()}, qualifier: receiver.Name, write: true, closure: scope.closure})
			}
		}
		child(node.Receiver)
	case *IndexExpr:
		child(node.Receiver)
		child(node.Index)
	case *IndexListExpr:
		child(node.Receiver)
		for _, index := range node.Indices {
			child(index)
		}
	case *SliceExpr:
		child(node.Receiver)
		child(node.Low)
		child(node.High)
		child(node.Max)
	case *TypeAssertExpr:
		child(node.Expression)
	case *PostfixExpr:
		child(node.Expression)
	case *SpreadExpr:
		child(node.Expression)
	case *SendExpr:
		child(node.Channel)
		child(node.Value)
	case *ParenthesizedExpr:
		visit.expression(node.Inner, scope, write)
	case *CallExpr:
		if name := simpleCallName(node.Callee); name != nil && !scope.contains(name.Name) {
			visit(classFieldUse{name: name, callee: true, closure: scope.closure})
			switch callee := node.Callee.(type) {
			case *IndexExpr:
				child(callee.Index)
			case *IndexListExpr:
				for _, index := range callee.Indices {
					child(index)
				}
			}
		} else {
			child(node.Callee)
		}
		for _, argument := range node.Arguments {
			child(argument.Value)
		}
	case *CompositeLiteralExpr:
		// A bare struct key is a declaration name. Map keys and array
		// indices are expressions.
		_, mapType := node.Type.(*MapType)
		_, arrayType := node.Type.(*ArrayType)
		for _, element := range node.Elements {
			_, identifier := element.Key.(*NameExpr)
			if mapType || arrayType || !identifier {
				child(element.Key)
			}
			child(element.Value)
		}
	case *InterpolatedStringExpr:
		for _, segment := range node.Segments {
			child(segment.Expression)
		}
	case *LambdaExpr:
		nested := newFieldLocalScope(scope)
		nested.closure = true
		parameters, _ := lambdaParametersFromTokens(node.Parameters)
		bindFieldLocals(nested, parameters)
		visit.expression(node.Body, nested, false)
		visit.block(node.BlockBody, nested)
	case *FunctionLiteralExpr:
		nested := newFieldLocalScope(scope)
		nested.closure = true
		if node.Type != nil {
			bindFieldLocals(nested, node.Type.Parameters)
		}
		visit.block(node.Body, nested)
	}
}

func scopedClassQualifier(expression ExprNode, scope *fieldLocalScope) string {
	switch receiver := expression.(type) {
	case *NameExpr:
		if !scope.contains(receiver.Name) {
			return receiver.Name
		}
	case *SelectorExpr:
		if root, ok := receiver.Receiver.(*NameExpr); ok && !scope.contains(root.Name) {
			return root.Name + "." + receiver.Name
		}
	}
	return ""
}

func simpleCallName(callee ExprNode) *NameExpr {
	switch indexed := callee.(type) {
	case *IndexExpr:
		callee = indexed.Receiver
	case *IndexListExpr:
		callee = indexed.Receiver
	}
	name, _ := callee.(*NameExpr)
	return name
}

func bindFieldLocals(scope *fieldLocalScope, parameters []ParameterNode) {
	for _, parameter := range parameters {
		scope.names[parameter.Name] = true
	}
}

func isFunctionType(node TypeNode) bool {
	_, ok := node.(*FunctionType)
	return ok
}

func validateStaticFieldOrder(class *ClassDecl) error {
	declared := map[string]*Field{}
	for index := range class.StaticFields {
		field := &class.StaticFields[index]
		declared[field.Name] = field
	}
	for _, item := range classInitializerOrder(class.StaticFields, class.StaticInitializers) {
		var problem error
		visit := classFieldVisitor(func(use classFieldUse) {
			field, exists := declared[use.name.Name]
			if problem != nil || !exists || use.qualifier != "" || field.Span().Start < item.span.Start || use.write ||
				(use.callee && !isFunctionType(field.TypeAST)) {
				return
			}
			if field == item.field {
				problem = diagnosticAt(class.SourceFile, use.name.Span(), fmt.Errorf("self-reference in initializer of static field %s", use.name.Name))
			} else {
				problem = diagnosticAt(class.SourceFile, use.name.Span(), fmt.Errorf("illegal forward reference to static field %s", use.name.Name))
			}
		})
		if item.field != nil {
			visit.expression(item.field.InitializerAST, newFieldLocalScope(nil), false)
		} else {
			visit.block(item.block, nil)
		}
		if problem != nil {
			return problem
		}
	}
	return nil
}

// Bind source names before type-dependent lowering, including overloaded
// calls. Both emission paths then use the existing qualified field lowering.
// Keep the parsed method intact, since inherited bodies can be emitted again.
func bindImplicitClassMembers(method Method, class *ClassDecl, context constructorContext) (Method, error) {
	if method.Owner == nil || method.BodyAST == nil {
		return method, nil
	}
	classes := classesForClass(context, class)
	staticMethods := context.StaticMethodSignatures[class.Name]
	if len(staticMethods) == 0 && !classHasStaticFields(class, classes, map[*ClassDecl]bool{}) {
		return method, nil
	}
	scope := newFieldLocalScope(nil)
	bindFieldLocals(scope, method.ParameterAST)
	bindFieldLocals(scope, method.ResultFieldsAST)
	for _, parameter := range method.TypeParamsAST {
		scope.names[parameter.Name] = true
	}
	if !method.IsStatic {
		for _, parameter := range class.TypeParamsAST {
			scope.names[parameter.Name] = true
		}
	}

	fieldReceiver := staticFieldScopePrefix + class.Name + "."
	callReceiver := classScopeCallPrefix + class.Name + "."
	if method.IsStatic {
		callReceiver = staticScopeCallPrefix + class.Name + "."
	}
	source := methodBodySource(method)
	locations := map[int]string{}
	fields := map[string]*Field{}
	visit := classFieldVisitor(func(use classFieldUse) {
		if use.qualifier != "" {
			return
		}
		name := use.name.Name
		position := use.name.Span().Start - method.BodySpan.Start
		if position < 0 || position+len(name) > len(source) || source[position:position+len(name)] != name {
			return
		}
		if use.callee && len(staticMethods[name]) != 0 && !packageCallNamed(name, context) {
			locations[position] = callReceiver
			return
		}
		field, lookedUp := fields[name]
		if !lookedUp {
			ref, _ := findStaticField(class, use.name.Name, "", classes, map[*ClassDecl]bool{})
			field = ref.field
			fields[use.name.Name] = field
		}
		if field == nil || (use.callee && !isFunctionType(field.TypeAST)) {
			return
		}
		locations[position] = fieldReceiver
	})
	visit.block(method.BodyAST, scope)
	if len(locations) == 0 {
		return method, nil
	}
	positions := make([]int, 0, len(locations))
	for position := range locations {
		positions = append(positions, position)
	}
	sort.Ints(positions)
	var body strings.Builder
	previous := 0
	for _, position := range positions {
		body.WriteString(source[previous:position])
		body.WriteString(locations[position])
		previous = position
	}
	body.WriteString(source[previous:])

	tokens, err := LexSource(method.Owner.Name, body.String())
	if err != nil {
		return method, err
	}
	owner := *method.Owner
	owner.Source = owner.Source[:method.BodySpan.Start] + body.String()
	method.Owner = &owner
	method.BodySpan.End = len(owner.Source)
	offsetTokenPositions(tokens, method.BodySpan)
	method.BodyTokens = tokens
	method.BodyAST, err = ParseBodyAST(method.BodyTokens)
	return method, err
}

// A same-package inherited body can be emitted on the derived Go receiver.
// Its static names still bind in the original source class.
func declaringClassForMemberLookup(class *ClassDecl, method Method, classes map[string]*ClassDecl) *ClassDecl {
	if method.Owner == nil || classDeclaresMethod(class, method) || method.Name == instanceInitializerBody {
		return class
	}
	for _, declaration := range method.Owner.Decls {
		candidate, ok := declaration.(*ClassDecl)
		if !ok || candidate.Span().Start > method.Span().Start || method.Span().End > candidate.Span().End {
			continue
		}
		for _, parent := range classes {
			if parent == candidate {
				return candidate
			}
		}
	}
	return class
}
