package compiler

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

const staticInitializerBody = "gppClassInitializer"
const staticFinalInitializerPrefix = "__gppStaticFinalInit_"
const classInitializationRuntimePath = "gpp/classinit"

type staticInitializationContext struct {
	LocalRuntimeEmitted bool
}

func staticInitializerOrder(class *ClassDecl) []instanceInitializer {
	items := classInitializerOrder(class.StaticFields, class.StaticInitializers)
	return slices.DeleteFunc(items, func(item instanceInitializer) bool {
		return item.field != nil && item.field.IsConstant
	})
}

func declaresStaticInitialization(class *ClassDecl) bool {
	return len(class.StaticInitializers) != 0 || slices.ContainsFunc(class.StaticFields, func(field Field) bool {
		return !field.IsConstant
	})
}

func programDeclaresStaticInitialization(files []*File) bool {
	for _, file := range files {
		for _, decl := range file.Decls {
			if class, ok := decl.(*ClassDecl); ok && declaresStaticInitialization(class) {
				return true
			}
		}
	}
	return false
}

func needsClassInitialization(class *ClassDecl, classes map[string]*ClassDecl, visiting map[*ClassDecl]bool) bool {
	if class == nil || visiting[class] {
		return false
	}
	if declaresStaticInitialization(class) {
		return true
	}
	visiting[class] = true
	defer delete(visiting, class)
	for _, name := range classParentNames(class) {
		if needsClassInitialization(classes[name], classes, visiting) {
			return true
		}
	}
	return false
}

func fileEmitsStaticInitialization(file *File, context constructorContext) bool {
	for _, decl := range file.Decls {
		if class, ok := decl.(*ClassDecl); ok && needsClassInitialization(class, classesForClass(context, class), map[*ClassDecl]bool{}) {
			return true
		}
	}
	return false
}

func classInitializationCall(class *ClassDecl, qualifier string) string {
	if qualifier != "" {
		qualifier += "."
	}
	return qualifier + "GppInitializeClass_" + class.Name
}

func staticMethodInitializationCall(class *ClassDecl, method Method, context constructorContext) string {
	if method.Name != staticInitializerBody && needsClassInitialization(class, classesForClass(context, class), map[*ClassDecl]bool{}) {
		return classInitializationCall(class, "")
	}
	return ""
}

func emitClassInitializationRuntime(filename string) error {
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		return err
	}
	source := "package classinit\n\nimport (\n\t\"runtime\"\n\t\"sync\"\n\t\"sync/atomic\"\n)\n" +
		classInitializationRuntime("runtime", "sync", "atomic", "") + localExceptionABI("Raise", "UnwrapRaised")
	return writeFileIfChanged(filename, []byte(source), 0644)
}

func classInitializationImports(body string) ([3]string, string) {
	existing, _, _ := generatedImports(body)
	var aliases [3]string
	var imports strings.Builder
	for index, importPath := range []string{"runtime", "sync", "sync/atomic"} {
		for _, spec := range existing {
			if spec.Path == importPath && spec.Alias != "_" && spec.Alias != "." {
				aliases[index] = spec.Alias
				break
			}
		}
		if aliases[index] == "" {
			aliases[index] = generatedImportAlias(body, "__gppClassInit"+filepath.Base(importPath))
			fmt.Fprintf(&imports, "import %s %s\n", aliases[index], strconv.Quote(importPath))
		}
	}
	return aliases, imports.String()
}

func emitStaticInitialization(out *strings.Builder, class *ClassDecl, classes map[string]*ClassDecl, context constructorContext, sourcePath, interpolationName string) error {
	if !needsClassInitialization(class, classes, map[*ClassDecl]bool{}) {
		return nil
	}
	stateType := "__gppClassInitialization"
	if context.ModulePath != "" {
		stateType = "__gppClassInitRuntime.ClassInitialization"
	}
	state := "__gppClassInit_" + class.Name
	guard := classInitializationCall(class, "")
	fmt.Fprintf(out, "var %s %s\n", state, stateType)
	fmt.Fprintf(out, "func %s() {\n", guard)
	fullName := class.Name
	if class.Owner != nil {
		fullName = class.Owner.Package + "." + fullName
	}
	fmt.Fprintf(out, "\t%s.Ensure(%q, func() {\n", state, fullName)
	for _, name := range classParentNames(class) {
		parent := classes[name]
		if needsClassInitialization(parent, classes, map[*ClassDecl]bool{}) {
			qualifier, _, qualified := strings.Cut(name, ".")
			if !qualified {
				qualifier = ""
			}
			fmt.Fprintf(out, "\t\t%s()\n", classInitializationCall(parent, qualifier))
		}
	}
	items := staticInitializerOrder(class)
	if len(items) != 0 {
		fmt.Fprintf(out, "\t\t%s()\n", staticMethodGoName(class, Method{Name: staticInitializerBody}))
	}
	out.WriteString("\t}, __gppUnwrapThrown, __gppThrow)\n}\n\n")
	for index := range class.StaticFields {
		field := &class.StaticFields[index]
		if field.IsConstant {
			continue
		}
		ref := staticFieldReference{owner: class, field: field}
		storage := ref.storageName()
		fieldType := transformPolymorphicType(fieldTypeSource(*field), context)
		fmt.Fprintf(out, "var %s %s\n", storage, fieldType)
		fmt.Fprintf(out, "func %s() %s { %s(); return %s }\n", ref.accessor("Get"), fieldType, guard, storage)
		if !field.IsFinal {
			fmt.Fprintf(out, "func %s(value %s) { %s(); %s = value }\n", ref.accessor("Set"), fieldType, guard, storage)
			fmt.Fprintf(out, "func %s() *%s { %s(); return &%s }\n", ref.accessor("Addr"), fieldType, guard, storage)
		}
		out.WriteByte('\n')
	}
	if len(items) == 0 {
		return nil
	}
	method, err := buildClassInitializerMethod(class, items, staticFieldScopePrefix+class.Name, staticInitializerBody, true)
	if err != nil {
		return err
	}
	return emitStaticMethod(out, sourcePath, class, method, context, interpolationName)
}

func validateStaticInitializers(class *ClassDecl, names map[string]bool) error {
	for index := range class.StaticFields {
		field := &class.StaticFields[index]
		if names[field.Name] {
			return fieldDiagnosticf(class, field, "class %s declares field %s more than once", class.Name, field.Name)
		}
		names[field.Name] = true
		if field.IsConstant && field.InitializerAST == nil {
			return fieldDiagnosticf(class, field, "static constant %s requires an initializer", field.Name)
		}
	}
	for index := range class.Methods {
		method := &class.Methods[index]
		if method.Name == staticInitializerBody {
			return methodDiagnosticf(class, method, "method %s is reserved for GPP class initialization", method.Name)
		}
	}
	for _, item := range staticInitializerOrder(class) {
		if item.block != nil {
			var returned *ReturnStmt
			walkInitializerStatements(item.block, func(statement Stmt) {
				if node, ok := statement.(*ReturnStmt); ok && returned == nil {
					returned = node
				}
			})
			if returned != nil {
				return diagnosticAt(class.SourceFile, returned.Span(), fmt.Errorf("return is invalid in a static initializer"))
			}
		}
		body := item.block
		if item.field != nil {
			body = &BlockStmt{Statements: []Stmt{&ExpressionStmt{Expression: item.field.InitializerAST}}}
		}
		var problem error
		walkIntrospectionBlockExpressions(body, func(expression ExprNode) {
			if name, ok := expression.(*NameExpr); ok && name.Name == "this" && problem == nil {
				problem = diagnosticAt(class.SourceFile, name.Span(), fmt.Errorf("a static initializer has no this"))
			}
		})
		if problem != nil {
			return problem
		}
	}
	if err := validateStaticFieldOrder(class); err != nil {
		return err
	}
	return validateStaticFinalFields(class)
}

type staticFieldReference struct {
	owner     *ClassDecl
	field     *Field
	qualifier string
}

func (ref staticFieldReference) storageName() string {
	return "__gppStaticField_" + ref.owner.Name + "_" + ref.field.Name
}

func (ref staticFieldReference) accessor(operation string) string {
	prefix := "Gpp"
	if memberVisibility(ref.field.Name) == memberPrivate {
		prefix = "__gpp"
	}
	if ref.qualifier != "" {
		prefix = ref.qualifier + "." + prefix
	}
	return prefix + operation + "Static_" + ref.owner.Name + "_" + ref.field.Name
}

func resolveStaticField(receiver, name string, context constructorContext) (staticFieldReference, bool) {
	target, exists := context.Targets[receiver]
	if !exists {
		class, bound := strings.CutPrefix(receiver, staticFieldScopePrefix)
		if !bound {
			class, bound = strings.CutPrefix(receiver, staticFinalInitializerPrefix)
		}
		if bound {
			target, exists = context.Targets[class]
		}
	}
	if !exists {
		return staticFieldReference{}, false
	}
	return findStaticField(target.Class, name, target.Qualifier, classesForClass(context, target.Class), map[*ClassDecl]bool{})
}

func findStaticField(class *ClassDecl, name, qualifier string, classes map[string]*ClassDecl, visiting map[*ClassDecl]bool) (staticFieldReference, bool) {
	if class == nil || visiting[class] {
		return staticFieldReference{}, false
	}
	if field, declared := lookupDeclaredStaticField(class, name); declared {
		if field != nil {
			return staticFieldReference{owner: class, field: field, qualifier: qualifier}, true
		}
		return staticFieldReference{}, false
	}
	visiting[class] = true
	defer delete(visiting, class)
	for _, parentName := range classParentNames(class) {
		parent := classes[parentName]
		parentQualifier := qualifier
		if alias, _, qualified := strings.Cut(parentName, "."); qualified {
			parentQualifier = alias
		}
		if ref, ok := findStaticField(parent, name, parentQualifier, classes, visiting); ok && memberVisibility(ref.field.Name) != memberPrivate {
			return ref, true
		}
	}
	return staticFieldReference{}, false
}

// An instance field with the same name hides an inherited static field.
func lookupDeclaredStaticField(class *ClassDecl, name string) (*Field, bool) {
	for _, field := range class.Fields {
		if field.Name == name {
			return nil, true
		}
	}
	for index := range class.StaticFields {
		if class.StaticFields[index].Name == name {
			return &class.StaticFields[index], true
		}
	}
	return nil, false
}

// The emitted Go still carries class-qualified field selectors from both
// method-lowering paths. Use Go's identifier resolution to distinguish class
// qualifiers from values with the same name and preserve source directives.
func rewriteStaticFields(body string, context constructorContext) (string, error) {
	hasFields := false
	for _, target := range context.Targets {
		if target.Class != nil && len(target.Class.StaticFields) != 0 {
			hasFields = true
			break
		}
	}
	if !hasFields {
		return body, nil
	}

	const header = "package staticfields\n"
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", header+body, 0)
	if err != nil {
		return "", fmt.Errorf("lower static fields: %w", err)
	}
	type replacement struct {
		start, end int
		text       string
	}
	var edits []replacement
	add := func(start, end token.Pos, text string) {
		edits = append(edits, replacement{
			start: fset.PositionFor(start, false).Offset - len(header),
			end:   fset.PositionFor(end, false).Offset - len(header),
			text:  text,
		})
	}
	fields := map[*ast.SelectorExpr]staticFieldReference{}
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if ref, ok := resolveStaticField(staticFieldQualifier(selector.X, file.Scope), selector.Sel.Name, context); ok {
			fields[selector] = ref
			return false
		}
		return true
	})
	address := map[*ast.SelectorExpr]bool{}
	for _, declaration := range file.Decls {
		function, _ := declaration.(*ast.FuncDecl)
		var invalidWrite error
		write := func(expression ast.Expr, assign bool) {
			selector, _ := ast.Unparen(expression).(*ast.SelectorExpr)
			ref, found := fields[selector]
			if !found || !ref.field.IsFinal || invalidWrite != nil {
				return
			}
			receiver := staticFieldQualifier(selector.X, file.Scope)
			prefix := staticFieldScopePrefix
			if ref.field.InitializerAST != nil {
				prefix = staticFinalInitializerPrefix
			}
			initializer := staticMethodGoName(ref.owner, Method{Name: staticInitializerBody})
			if assign && function != nil && function.Name.Name == initializer && ref.qualifier == "" && receiver == prefix+ref.owner.Name {
				add(selector.Pos(), selector.End(), ref.storageName())
				delete(fields, selector)
				return
			}
			location := fset.Position(selector.Pos())
			invalidWrite = diagnosticAt(location.Filename, Span{Line: location.Line, Column: location.Column},
				fmt.Errorf("cannot modify static final field %s.%s", ref.owner.Name, ref.field.Name))
		}
		ast.Inspect(declaration, func(node ast.Node) bool {
			if invalidWrite != nil {
				return false
			}
			switch value := node.(type) {
			case *ast.AssignStmt:
				if len(value.Lhs) == 1 && len(value.Rhs) == 1 && value.Tok == token.ASSIGN {
					field, _ := ast.Unparen(value.Lhs[0]).(*ast.SelectorExpr)
					if ref, ok := fields[field]; ok && !ref.field.IsConstant && !ref.field.IsFinal {
						// Go evaluates the argument before entering the setter,
						// matching putstatic after Java's RHS evaluation.
						add(value.Lhs[0].Pos(), value.Rhs[0].Pos(), ref.accessor("Set")+"(")
						add(value.Rhs[0].End(), value.Rhs[0].End(), ")")
						delete(fields, field)
					}
				}
				for _, lhs := range value.Lhs {
					write(lhs, value.Tok == token.ASSIGN)
					markStaticFieldAddress(lhs, address)
				}
			case *ast.IncDecStmt:
				write(value.X, false)
				markStaticFieldAddress(value.X, address)
			case *ast.RangeStmt:
				write(value.Key, false)
				write(value.Value, false)
				markStaticFieldAddress(value.Key, address)
				markStaticFieldAddress(value.Value, address)
			case *ast.UnaryExpr:
				if value.Op == token.AND {
					write(value.X, false)
					markStaticFieldAddress(value.X, address)
				}
			case *ast.SelectorExpr:
				// Preserve addressability of struct members and method receivers.
				markStaticFieldAddress(value.X, address)
			case *ast.IndexExpr:
				markStaticFieldAddress(value.X, address)
			}
			return true
		})
		if invalidWrite != nil {
			return "", invalidWrite
		}
	}
	for selector, ref := range fields {
		access := ref.accessor("Get") + "()"
		if ref.field.IsConstant {
			access = ref.constantName()
		} else if address[selector] && !ref.field.IsFinal {
			access = "(*" + ref.accessor("Addr") + "())"
		}
		add(selector.Pos(), selector.End(), access)
	}
	sort.Slice(edits, func(i, j int) bool {
		if edits[i].start == edits[j].start {
			return edits[i].end < edits[j].end
		}
		return edits[i].start < edits[j].start
	})
	var result strings.Builder
	cursor := 0
	for _, edit := range edits {
		result.WriteString(body[cursor:edit.start])
		result.WriteString(edit.text)
		cursor = edit.end
	}
	result.WriteString(body[cursor:])
	return result.String(), nil
}

func markStaticFieldAddress(expression ast.Expr, address map[*ast.SelectorExpr]bool) {
	if selector, ok := ast.Unparen(expression).(*ast.SelectorExpr); ok {
		address[selector] = true
	}
}

func staticFieldQualifier(expression ast.Expr, fileScope *ast.Scope) string {
	switch value := expression.(type) {
	case *ast.Ident:
		if value.Obj == nil || (fileScope != nil && value.Obj.Kind == ast.Typ && value.Obj == fileScope.Lookup(value.Name)) {
			return value.Name
		}
	case *ast.SelectorExpr:
		if pkg, ok := value.X.(*ast.Ident); ok && pkg.Obj == nil {
			return pkg.Name + "." + value.Sel.Name
		}
	}
	return ""
}
