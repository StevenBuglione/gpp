package compiler

import (
	"go/ast"
	"go/token"
	"strconv"
)

type tryBranchTransfers struct {
	state     *ast.Ident
	transfers []*ast.BranchStmt
}

type tryBranchTarget struct {
	label string
	loop  bool
}

// The try body and each catch run in generated functions. A branch to a target
// outside one of those functions records its destination and leaves the
// function. Dispatch happens after the wrapper, so finally runs first.
func lowerTryBranchTransfers(body *ast.BlockStmt, clauses []catchClause) tryBranchTransfers {
	branches := tryBranchTransfers{state: ast.NewIdent("")}
	branches.rewriteList(body.List, nil)
	for _, clause := range clauses {
		if clause.body != nil {
			branches.rewriteList(clause.body.List, nil)
		}
	}
	return branches
}

func (branches *tryBranchTransfers) rewriteList(statements []ast.Stmt, targets []tryBranchTarget) {
	for index, statement := range statements {
		statements[index] = branches.rewrite(statement, targets, "")
	}
}

// Walk statement bodies only. A source function literal or an inner try
// wrapper has its own control-flow scope. An inner try's dispatch is outside
// its wrapper and can itself cross this try's boundary.
func (branches *tryBranchTransfers) rewrite(statement ast.Stmt, targets []tryBranchTarget, label string) ast.Stmt {
	switch node := statement.(type) {
	case *ast.LabeledStmt:
		node.Stmt = branches.rewrite(node.Stmt, targets, node.Label.Name)
	case *ast.BlockStmt:
		branches.rewriteList(node.List, targets)
	case *ast.IfStmt:
		branches.rewriteList(node.Body.List, targets)
		node.Else = branches.rewrite(node.Else, targets, "")
	case *ast.ForStmt:
		branches.rewriteList(node.Body.List, append(targets, tryBranchTarget{label: label, loop: true}))
	case *ast.RangeStmt:
		branches.rewriteList(node.Body.List, append(targets, tryBranchTarget{label: label, loop: true}))
	case *ast.SwitchStmt:
		branches.rewriteList(node.Body.List, append(targets, tryBranchTarget{label: label}))
	case *ast.TypeSwitchStmt:
		branches.rewriteList(node.Body.List, append(targets, tryBranchTarget{label: label}))
	case *ast.SelectStmt:
		branches.rewriteList(node.Body.List, append(targets, tryBranchTarget{label: label}))
	case *ast.CaseClause:
		branches.rewriteList(node.Body, targets)
	case *ast.CommClause:
		branches.rewriteList(node.Body, targets)
	case *ast.BranchStmt:
		if node.Tok != token.BREAK && node.Tok != token.CONTINUE {
			break
		}
		for _, target := range targets {
			if node.Label != nil {
				// Let Go diagnose continuing to a local non-loop label.
				if node.Label.Name == target.label {
					return node
				}
			} else if node.Tok == token.BREAK || target.loop {
				return node
			}
		}
		branches.transfers = append(branches.transfers, node)
		return &ast.BlockStmt{List: []ast.Stmt{branches.assign(len(branches.transfers)), &ast.ReturnStmt{}}}
	}
	return statement
}

func (branches tryBranchTransfers) assign(index int) ast.Stmt {
	return &ast.AssignStmt{
		Lhs: []ast.Expr{branches.state}, Tok: token.ASSIGN,
		Rhs: []ast.Expr{&ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(index)}},
	}
}

func (branches tryBranchTransfers) dispatchAfter(run ast.Stmt) ast.Stmt {
	if len(branches.transfers) == 0 {
		return run
	}
	// Include catch bindings and types, user expressions and nested try
	// state variables when choosing a name for the captured branch state.
	names := map[string]bool{}
	ast.Inspect(run, func(node ast.Node) bool {
		if ident, ok := node.(*ast.Ident); ok {
			names[ident.Name] = true
		}
		return true
	})
	branches.state.Name = "__gppTryBranch"
	for names[branches.state.Name] {
		branches.state.Name += "_"
	}
	declaration := &ast.AssignStmt{
		Lhs: []ast.Expr{branches.state}, Tok: token.DEFINE,
		Rhs: []ast.Expr{&ast.BasicLit{Kind: token.INT, Value: "0"}},
	}
	result := &ast.BlockStmt{List: []ast.Stmt{declaration, run}}
	for index, branch := range branches.transfers {
		result.List = append(result.List, &ast.IfStmt{
			Cond: &ast.BinaryExpr{
				X: branches.state, Op: token.EQL,
				Y: &ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(index + 1)},
			},
			Body: &ast.BlockStmt{List: []ast.Stmt{branch}},
		})
	}
	return result
}
