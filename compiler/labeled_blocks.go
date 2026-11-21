package compiler

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strings"
)

func hasLabeledBlock(tokens []Token) bool {
	for index, current := range tokens {
		if current.Text != ":" {
			continue
		}
		next := nextSignificantToken(tokens, index+1)
		if next < len(tokens) && tokens[next].Text == "{" {
			return true
		}
	}
	return false
}

func validateFileLabeledBlocks(file *File) error {
	if strings.HasSuffix(file.Name, ".go") || !hasLabeledBlock(file.Tokens) {
		return nil
	}
	var problem error
	checkScope := func(block *BlockStmt) {
		if problem == nil {
			problem = validateBlockLabelScopes(block, sourceDirectivePath(file))
		}
	}
	checkLiterals := func(expression ExprNode) {
		collectMemberVisibilityExprs(expression, func(candidate ExprNode) {
			switch literal := candidate.(type) {
			case *FunctionLiteralExpr:
				checkScope(literal.Body)
			case *LambdaExpr:
				checkScope(literal.BlockBody)
			}
		})
	}
	checkBlock := func(block *BlockStmt) {
		checkScope(block)
		walkMemberVisibilityBlockExpressions(block, checkLiterals)
	}
	checkMethods := func(methods []Method) {
		for _, method := range methods {
			checkBlock(method.BodyAST)
		}
	}
	for _, declaration := range file.Decls {
		switch node := declaration.(type) {
		case *FunctionDecl:
			checkBlock(node.Method.BodyAST)
		case *MixedDecl:
			for _, function := range node.Functions {
				if function != nil {
					checkBlock(function.Method.BodyAST)
				}
			}
		case *ClassDecl:
			checkMethods(node.Methods)
			for _, block := range node.InstanceInitializers {
				checkBlock(block)
			}
			for _, block := range node.StaticInitializers {
				checkBlock(block)
			}
			for _, field := range node.Fields {
				checkLiterals(field.InitializerAST)
			}
			for _, field := range node.StaticFields {
				checkLiterals(field.InitializerAST)
			}
		case *ExtendDecl:
			checkMethods(node.Methods)
		case *ValueDecl:
			for _, value := range node.Values {
				checkLiterals(value)
			}
		}
		if problem != nil {
			return problem
		}
	}
	return nil
}

// Source control-flow scopes include try, catch, and finally bodies. Check
// overlapping labels before exception lowering creates synthetic functions.
func validateBlockLabelScopes(block *BlockStmt, filename string) error {
	type activeLabel struct {
		name  string
		block bool
	}
	var visitBlock func(*BlockStmt, []activeLabel) error
	var visitStmt func(Stmt, []activeLabel) error
	visitBlock = func(body *BlockStmt, active []activeLabel) error {
		if body == nil {
			return nil
		}
		var labels []*LabelStmt
		for _, statement := range body.Statements {
			if label, ok := statement.(*LabelStmt); ok {
				labels = append(labels, label)
				continue
			}
			_, explicitBlock := statement.(*BlockStmt)
			scope := active
			for _, label := range labels {
				for _, enclosing := range scope {
					if enclosing.name == label.Name && (enclosing.block || explicitBlock) {
						return diagnosticAt(filename, label.Span(), fmt.Errorf("duplicate enclosing label %s", label.Name))
					}
				}
				scope = append(scope, activeLabel{name: label.Name, block: explicitBlock})
			}
			if err := visitStmt(statement, scope); err != nil {
				return err
			}
			labels = labels[:0]
		}
		return nil
	}
	visitStmt = func(statement Stmt, active []activeLabel) error {
		if statement == nil || isNilStmt(statement) {
			return nil
		}
		var bodies []*BlockStmt
		switch node := statement.(type) {
		case *BlockStmt:
			bodies = append(bodies, node)
		case *ForStmt:
			bodies = append(bodies, node.Body)
		case *SwitchStmt:
			bodies = append(bodies, node.Body)
		case *CaseStmt:
			bodies = append(bodies, node.Clause.Body)
		case *IfStmt:
			bodies = append(bodies, node.Body, node.Else)
			if err := visitStmt(node.ElseIf, active); err != nil {
				return err
			}
		case *TryStmt:
			bodies = append(bodies, node.Body, node.Finally)
			for _, clause := range node.Catches {
				bodies = append(bodies, clause.Body)
			}
		case *TokenStmt:
			bodies = append(bodies, node.Body)
			for _, child := range node.Children {
				if err := visitStmt(child, active); err != nil {
					return err
				}
			}
		}
		for _, body := range bodies {
			if err := visitBlock(body, active); err != nil {
				return err
			}
		}
		return nil
	}
	return visitBlock(block, nil)
}

func labeledGoBlock(label *ast.LabeledStmt) *ast.BlockStmt {
	for {
		switch statement := label.Stmt.(type) {
		case *ast.BlockStmt:
			return statement
		case *ast.LabeledStmt:
			label = statement
		default:
			return nil
		}
	}
}

// Lower a named block break to an exit from that block. Doing this after the
// try pass lets finally run before an enclosing block receives its break.
// Keep a separately referenced Go goto label at the original entry.
func lowerLabeledBlocks(body string, source *File) (string, error) {
	if strings.HasSuffix(source.Name, ".go") || !hasLabeledBlock(source.Tokens) {
		return body, nil
	}
	const header = "package gppblocklabels\n"
	fileset := token.NewFileSet()
	file, err := parser.ParseFile(fileset, sourceDirectivePath(source), header+body, 0)
	if err != nil {
		return "", fmt.Errorf("lower labeled blocks: %w", err)
	}
	type edit struct {
		start, end int
		text       string
	}
	var edits []edit
	add := func(start, end token.Pos, text string) {
		edits = append(edits, edit{
			start: fileset.PositionFor(start, false).Offset - len(header),
			end:   fileset.PositionFor(end, false).Offset - len(header),
			text:  text,
		})
	}
	diagnose := func(label *ast.Ident, message string) error {
		position := fileset.Position(label.Pos())
		return diagnosticAt(position.Filename, Span{Line: position.Line, Column: position.Column}, fmt.Errorf("%s %s", message, label.Name))
	}
	checkFunction := func(root *ast.BlockStmt) error {
		if root == nil {
			return nil
		}
		names := map[string]bool{}
		labels := map[string]int{}
		gotos := map[string]*ast.Ident{}
		var blockLabels []*ast.LabeledStmt
		type transfer struct {
			branch *ast.BranchStmt
			block  *ast.BlockStmt
		}
		var transfers []transfer
		var problem error
		ast.PreorderStack(root, nil, func(node ast.Node, stack []ast.Node) bool {
			switch current := node.(type) {
			case *ast.FuncLit:
				return false
			case *ast.Ident:
				names[current.Name] = true
			case *ast.LabeledStmt:
				labels[current.Label.Name]++
				explicitBlock := labeledGoBlock(current) != nil
				if explicitBlock {
					blockLabels = append(blockLabels, current)
				}
				for _, ancestor := range stack {
					label, ok := ancestor.(*ast.LabeledStmt)
					if ok && label.Label.Name == current.Label.Name &&
						(explicitBlock || labeledGoBlock(label) != nil) && problem == nil {
						problem = diagnose(current.Label, "duplicate enclosing label")
					}
				}
			case *ast.BranchStmt:
				if current.Label == nil {
					break
				}
				if current.Tok == token.GOTO {
					gotos[current.Label.Name] = current.Label
					break
				}
				for index := len(stack) - 1; index >= 0; index-- {
					label, ok := stack[index].(*ast.LabeledStmt)
					if !ok || label.Label.Name != current.Label.Name {
						continue
					}
					if block := labeledGoBlock(label); block != nil {
						switch current.Tok {
						case token.BREAK:
							transfers = append(transfers, transfer{branch: current, block: block})
						case token.CONTINUE:
							if problem == nil {
								problem = diagnose(current.Label, "continue requires a loop, invalid target")
							}
						}
					}
					break
				}
			}
			return true
		})
		if problem != nil {
			return problem
		}
		for _, label := range blockLabels {
			name := label.Label.Name
			if target := gotos[name]; target != nil {
				if labels[name] > 1 {
					return diagnose(target, "ambiguous goto label")
				}
				continue
			}
			add(label.Pos(), label.Colon+1, "")
		}
		exits := map[*ast.BlockStmt]string{}
		for _, transfer := range transfers {
			exit := exits[transfer.block]
			if exit == "" {
				exit = nextExceptionName(names, "__gppBlockExit")
				exits[transfer.block] = exit
				at := transfer.block.End()
				add(at, at, "; "+exit+": ;")
			}
			add(transfer.branch.Pos(), transfer.branch.Pos()+token.Pos(len("break")), "goto")
			add(transfer.branch.Label.Pos(), transfer.branch.Label.End(), exit)
		}
		return nil
	}

	var problem error
	ast.Inspect(file, func(node ast.Node) bool {
		if problem != nil {
			return false
		}
		switch function := node.(type) {
		case *ast.FuncDecl:
			problem = checkFunction(function.Body)
		case *ast.FuncLit:
			problem = checkFunction(function.Body)
		}
		return true
	})
	if problem != nil {
		return "", problem
	}
	if len(edits) == 0 {
		return body, nil
	}
	slices.SortFunc(edits, func(left, right edit) int { return left.start - right.start })
	var result strings.Builder
	cursor := 0
	for _, change := range edits {
		result.WriteString(body[cursor:change.start])
		result.WriteString(change.text)
		cursor = change.end
	}
	result.WriteString(body[cursor:])
	return result.String(), nil
}
