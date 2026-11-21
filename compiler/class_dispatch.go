package compiler

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"slices"
	"strings"
)

const bindClassObjectMethod = "GppBindDynamicObject"
const ownsClassReceiverMethod = "GppOwnsReceiver"
const classDispatchPrefix = "__gppVirtual_"

type classDispatch struct {
	methods map[string]Method
	used    map[string]bool
}

func newClassDispatch(methods []Method, context constructorContext) classDispatch {
	if context.Introspection == nil || !context.Introspection.Enabled {
		return classDispatch{}
	}
	dispatch := classDispatch{methods: map[string]Method{}, used: map[string]bool{}}
	for _, method := range methods {
		if method.Name != "init" && !method.IsStatic && !method.Generated &&
			memberVisibleToSubclass(method.Name) && len(method.TypeParamsAST) == 0 {
			dispatch.methods[methodOutputName(method)] = method
		}
	}
	return dispatch
}

// rewrite changes calls and method values on the current receiver. A
// parent-qualified receiver such as this.Parent retains its static target.
func (dispatch classDispatch) rewrite(source string, fullDeclaration bool) string {
	if len(dispatch.methods) == 0 {
		return source
	}
	prefix, suffix := "package p\n", ""
	if !fullDeclaration {
		prefix += "func body() {\n"
		suffix = "\n}"
	}
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "", prefix+source+suffix, parser.SkipObjectResolution)
	if err != nil {
		return source
	}
	var edits []int
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		receiver, ok := selector.X.(*ast.Ident)
		if _, virtual := dispatch.methods[selector.Sel.Name]; ok && receiver.Name == "this" && virtual {
			dispatch.used[selector.Sel.Name] = true
			edits = append(edits, fileSet.PositionFor(selector.Sel.Pos(), false).Offset-len(prefix))
		}
		return true
	})
	slices.Sort(edits)
	for _, offset := range slices.Backward(edits) {
		source = source[:offset] + classDispatchPrefix + source[offset:]
	}
	return source
}

func (dispatch classDispatch) emit(out *strings.Builder, class *ClassDecl, classes map[string]*ClassDecl, context constructorContext) error {
	for _, name := range slices.Sorted(maps.Keys(dispatch.used)) {
		method := dispatch.methods[name]
		parent, imported := importedParentForMethod(class, method, classes)
		parameters, err := parameterInfosForMethod(method)
		if err != nil {
			return err
		}
		parts := make([]string, 0, len(parameters))
		arguments := make([]string, 0, len(parameters))
		for index, parameter := range parameters {
			typeName := parameter.typeText()
			if imported {
				typeName = qualifyInheritedMethodTypes(typeName, method, parent, classes, context)
			}
			argument := fmt.Sprintf("arg%d", index)
			parts = append(parts, argument+" "+transformPolymorphicType(typeName, context))
			if strings.HasPrefix(typeName, "...") {
				argument += "..."
			}
			arguments = append(arguments, argument)
		}
		params := strings.Join(parts, ", ")
		args := strings.Join(arguments, ", ")
		result := strings.TrimSpace(methodResultSource(method))
		if len(method.ResultFieldsAST) != 0 {
			resultTypes := make([]string, 0, len(method.ResultFieldsAST))
			for _, field := range method.ResultFieldsAST {
				typeName, err := typeNodeSource(field.Type)
				if err != nil {
					return err
				}
				resultTypes = append(resultTypes, typeName)
			}
			result = "(" + strings.Join(resultTypes, ", ") + ")"
		}
		if imported {
			result = qualifyInheritedMethodTypes(result, method, parent, classes, context)
		}
		result = transformPolymorphicResultType(result, context)
		returnPrefix := ""
		if result != "" {
			result = " " + result
			returnPrefix = "return "
		}
		fmt.Fprintf(out, "func (this *%s%s) %s%s(%s)%s {\n",
			class.Name, typeParameterNames(class.TypeParamsAST), classDispatchPrefix, name, params, result)
		fmt.Fprintf(out, "\tif target, ok := this.GppDynamicObject.(interface { %s(any) bool; %s(%s)%s }); ok && this.GppDynamicObject != this && target.%s(this) {\n",
			ownsClassReceiverMethod, name, params, result, ownsClassReceiverMethod)
		fmt.Fprintf(out, "\t\t%starget.%s(%s)\n", returnPrefix, name, args)
		if returnPrefix == "" {
			out.WriteString("\t\treturn\n")
		}
		fmt.Fprintf(out, "\t}\n\t%sthis.%s(%s)\n}\n\n", returnPrefix, name, args)
	}
	return nil
}

func emitDynamicObjectBinding(out *strings.Builder, class *ClassDecl) {
	classType := class.Name + typeParameterNames(class.TypeParamsAST)
	fmt.Fprintf(out, "func (this *%s) %s(object any) {\n", classType, bindClassObjectMethod)
	out.WriteString("\tthis.GppDynamicObject = object\n")
	for _, parent := range classParentNames(class) {
		fmt.Fprintf(out, "\tthis.%s.%s(object)\n", classParentFieldName(parent), bindClassObjectMethod)
	}
	out.WriteString("}\n\n")

	// An ordinary Go value copy retains the old dynamic object field. Verify
	// that the receiver is still part of that object before using the binding.
	fmt.Fprintf(out, "func (this *%s) %s(receiver any) bool {\n", classType, ownsClassReceiverMethod)
	out.WriteString("\treturn receiver == this")
	for _, parent := range classParentNames(class) {
		fmt.Fprintf(out, " || this.%s.%s(receiver)", classParentFieldName(parent), ownsClassReceiverMethod)
	}
	out.WriteString("\n}\n\n")
}
