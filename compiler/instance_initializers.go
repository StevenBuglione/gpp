package compiler

import (
	"cmp"
	"fmt"
	"slices"
	"sort"
	"strings"
)

const instanceInitializerBody = "gppInitInstanceBody"
const instanceInitializerMethod = "GppInitializeInstance"
const instanceInitializerWithError = "__gppInitInstanceError"

type instanceInitializer struct {
	span  Span
	field *Field
	block *BlockStmt
}

func hasInstanceInitializers(class *ClassDecl) bool {
	if class == nil {
		return false
	}
	return len(class.InstanceInitializers) != 0 || slices.ContainsFunc(class.Fields, func(field Field) bool {
		return field.InitializerAST != nil
	})
}

func instanceInitializerOrder(class *ClassDecl) []instanceInitializer {
	return classInitializerOrder(class.Fields, class.InstanceInitializers)
}

func classInitializerOrder(fields []Field, blocks []*BlockStmt) []instanceInitializer {
	var result []instanceInitializer
	for index := range fields {
		field := &fields[index]
		if field.InitializerAST != nil {
			result = append(result, instanceInitializer{span: field.Span(), field: field})
		}
	}
	for _, block := range blocks {
		result = append(result, instanceInitializer{span: block.Span(), block: block})
	}
	slices.SortFunc(result, func(left, right instanceInitializer) int { return cmp.Compare(left.span.Start, right.span.Start) })
	return result
}

func classInitializerTokens(class *ClassDecl) []Token {
	if class.Owner == nil {
		return nil
	}
	var tokens []Token
	for _, item := range append(instanceInitializerOrder(class), staticInitializerOrder(class)...) {
		span := item.span
		if item.field != nil {
			span = item.field.InitializerSpan
		}
		start := sort.Search(len(class.Owner.Tokens), func(index int) bool {
			return class.Owner.Tokens[index].Span.Start >= span.Start
		})
		for _, token := range class.Owner.Tokens[start:] {
			if token.Span.Start >= span.End {
				break
			}
			tokens = append(tokens, token)
		}
	}
	return tokens
}

func classMethodsForMemberValidation(class *ClassDecl) []Method {
	tokens := classInitializerTokens(class)
	if len(tokens) == 0 {
		return class.Methods
	}
	return append(slices.Clone(class.Methods), Method{
		Name:       "class initializer",
		Owner:      class.Owner,
		SpanValue:  tokens[0].Span,
		BodyTokens: tokens,
	})
}

// Lower instance initialization through the ordinary method pipeline so
// dispatch, implicit receiver access and exceptions match constructor bodies.
// Keep the original line positions for diagnostics and debugging.
func buildInstanceInitializerMethod(class *ClassDecl) (Method, error) {
	return buildClassInitializerMethod(class, instanceInitializerOrder(class), "this", instanceInitializerBody, false)
}

func buildClassInitializerMethod(class *ClassDecl, items []instanceInitializer, receiver, name string, static bool) (Method, error) {
	if class.Owner == nil {
		return Method{}, classDiagnosticf(class, "initializers require a class source file")
	}
	var body strings.Builder
	line := 1
	for _, item := range items {
		for line < item.span.Line {
			body.WriteByte('\n')
			line++
		}
		span := item.span
		if item.field != nil {
			span = item.field.InitializerSpan
			target := receiver
			if static && item.field.IsFinal {
				target = staticFinalInitializerPrefix + class.Name
			}
			fmt.Fprintf(&body, "%s.%s =", target, item.field.Name)
			for line < span.Line {
				body.WriteByte('\n')
				line++
			}
			body.WriteByte(' ')
		}
		if span.Start < 0 || span.Start > span.End || span.End > len(class.Owner.Source) {
			return Method{}, classDiagnosticf(class, "invalid initializer source span")
		}
		text := class.Owner.Source[span.Start:span.End]
		body.WriteString(text)
		body.WriteByte(';')
		line += strings.Count(text, "\n")
	}
	source := body.String()
	owner := &File{Name: class.Owner.Name, Package: class.Owner.Package, Source: source}
	tokens, err := LexSource(owner.Name, source)
	if err != nil {
		return Method{}, err
	}
	block, err := ParseBodyAST(tokens)
	if err != nil {
		return Method{}, err
	}
	return Method{
		Name:       name,
		IsStatic:   static,
		Owner:      owner,
		SpanValue:  items[0].span,
		BodySpan:   sourceSpan(source, 0, len(source)),
		BodyTokens: tokens,
		BodyAST:    block,
	}, nil
}

func emitInstanceInitializerMethods(out *strings.Builder, class *ClassDecl, acceptsError bool) {
	classType := class.Name + typeParameterNames(class.TypeParamsAST)
	fmt.Fprintf(out, "func (this *%s) %s() { this.%s() }\n\n",
		classType, instanceInitializerMethod, instanceInitializerBody)
	if acceptsError {
		fmt.Fprintf(out, "func (this *%s) %s() (failure error) {\n", classType, instanceInitializerWithError)
		out.WriteString("\tdefer func() {\n")
		out.WriteString("\t\tif recovered := recover(); recovered != nil {\n")
		out.WriteString("\t\t\tif thrown, ok := __gppUnwrapThrown(recovered); ok { failure = thrown; return }\n")
		out.WriteString("\t\t\tpanic(recovered)\n\t\t}\n\t}()\n")
		fmt.Fprintf(out, "\tthis.%s()\n\treturn nil\n}\n\n", instanceInitializerBody)
	}
}

func instanceInitializerCall(context constructorContext) Stmt {
	helper := instanceInitializerMethod
	if result, _ := typeNodeSource(context.CurrentResultAST); strings.TrimSpace(result) == "error" {
		helper = instanceInitializerWithError
	}
	call := &CallExpr{Callee: &SelectorExpr{Receiver: &NameExpr{Name: "this"}, Name: helper}}
	if helper == instanceInitializerWithError {
		return forwardInitializerFailure(call, context)
	}
	return &ExpressionStmt{Expression: call}
}

func validateInstanceInitializers(class *ClassDecl) error {
	if !hasInstanceInitializers(class) {
		return nil
	}
	positions := make(map[string]int, len(class.Fields))
	for _, field := range class.Fields {
		positions[field.Name] = field.Span().Start
	}
	for _, item := range instanceInitializerOrder(class) {
		body := item.block
		if item.field != nil {
			body = &BlockStmt{Statements: []Stmt{&ExpressionStmt{Expression: item.field.InitializerAST}}}
		}
		locals := initializerLocalNames(&Method{BodyAST: body})
		assignmentTargets := map[*NameExpr]bool{}
		var problem error
		walkInitializerStatements(body, func(statement Stmt) {
			switch node := statement.(type) {
			case *ReturnStmt:
				if problem == nil {
					problem = sourceLineError(node.Span(), fmt.Errorf("return is invalid in an instance initializer"))
				}
			case *AssignmentStmt:
				if node.Operator == "=" {
					for _, target := range node.Left {
						if name, ok := target.(*NameExpr); ok {
							assignmentTargets[name] = true
						}
					}
				}
			}
		})
		literalKeys := map[*NameExpr]bool{}
		walkIntrospectionBlockExpressions(body, func(expression ExprNode) {
			if problem != nil {
				return
			}
			switch node := expression.(type) {
			case *CallExpr:
				if isThisInitializerCall(node) || isSuperInitializerCall(node) {
					problem = sourceLineError(node.Span(), fmt.Errorf("a constructor invocation belongs in an init method"))
				}
			case *CompositeLiteralExpr:
				for _, element := range node.Elements {
					if key, ok := element.Key.(*NameExpr); ok {
						literalKeys[key] = true
					}
				}
			case *NameExpr:
				position, isField := positions[node.Name]
				if isField && position >= item.span.Start && !locals[node.Name] && !assignmentTargets[node] && !literalKeys[node] {
					problem = sourceLineError(node.Span(), fmt.Errorf("illegal forward reference to instance field %s", node.Name))
				}
			}
		})
		if problem != nil {
			return problem
		}
	}
	return nil
}
