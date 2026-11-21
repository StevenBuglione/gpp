package compiler

import (
	"fmt"
	"strings"
)

func classHasStaticFields(class *ClassDecl, classes map[string]*ClassDecl, visited map[*ClassDecl]bool) bool {
	if class == nil || visited[class] {
		return false
	}
	if len(class.StaticFields) != 0 {
		return true
	}
	visited[class] = true
	for _, name := range classParentNames(class) {
		if classHasStaticFields(classes[name], classes, visited) {
			return true
		}
	}
	return false
}

func (ref staticFieldReference) constantName() string {
	prefix := "GppConst_"
	if memberVisibility(ref.field.Name) == memberPrivate {
		prefix = "__gppConst_"
	}
	if ref.qualifier != "" {
		prefix = ref.qualifier + "." + prefix
	}
	return prefix + ref.owner.Name + "_" + ref.field.Name
}

func emitClassConstants(out *strings.Builder, class *ClassDecl, context constructorContext, filename string) error {
	for index := range class.StaticFields {
		field := &class.StaticFields[index]
		if !field.IsConstant {
			continue
		}
		if class.Owner == nil || field.InitializerAST == nil {
			return fieldDiagnosticf(class, field, "static constant %s requires a source initializer", field.Name)
		}

		// Reuse class name binding for the initializer while retaining its
		// original source span and the existing Go constant expression grammar.
		method := Method{
			Name: "static constant " + field.Name, IsStatic: true, Owner: class.Owner,
			BodySpan: field.InitializerSpan,
			BodyAST:  &BlockStmt{Statements: []Stmt{&ExpressionStmt{Expression: field.InitializerAST}}},
		}
		bound, err := bindImplicitClassMembers(method, class, context)
		if err != nil {
			return err
		}
		initializer := methodBodySource(bound)
		initializer, err = transformEnums(initializer, context)
		if err != nil {
			return err
		}
		ref := staticFieldReference{owner: class, field: field}
		emitSourceDirective(out, filename, field.Span().Line)
		fmt.Fprintf(out, "const %s %s = %s\n\n", ref.constantName(), fieldTypeSource(*field), initializer)
	}
	return nil
}
