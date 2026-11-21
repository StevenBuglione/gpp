package compiler

import (
	"fmt"
	"go/ast"
	"go/types"
	"path"
	"strings"
)

type staticFieldVisibility struct {
	model      *SemanticModel
	modulePath string
	enabled    bool
	imports    map[*File]map[string]*PackageSymbols
}

func (check *staticFieldVisibility) importsForFile(file *File) map[string]*PackageSymbols {
	if imports, ok := check.imports[file]; ok {
		return imports
	}
	imports := make(map[string]*PackageSymbols, len(file.Imports))
	for _, declaration := range file.Imports {
		alias := declaration.Alias
		if alias == "" {
			alias = path.Base(declaration.Path)
		}
		logical, _ := logicalPackageForImport(declaration.Path, check.modulePath, check.model)
		imports[alias] = check.model.Packages[logical]
	}
	if check.imports == nil {
		check.imports = map[*File]map[string]*PackageSymbols{}
	}
	check.imports[file] = imports
	return imports
}

func (check *staticFieldVisibility) class(file *File, name string) *ClassDecl {
	if file == nil {
		return nil
	}
	pkg := check.model.Packages[file.Package]
	if alias, member, qualified := strings.Cut(name, "."); qualified {
		pkg = check.importsForFile(file)[alias]
		name = member
	}
	if pkg == nil {
		return nil
	}
	return pkg.Classes[name]
}

// Follow each ancestor's own imports. An import alias in the caller can
// refer to a different package from the same alias in the declaring file.
func (check *staticFieldVisibility) lookup(class *ClassDecl, name string, visiting map[*ClassDecl]bool) (*ClassDecl, *Field) {
	if class == nil || visiting[class] {
		return nil, nil
	}
	if field, declared := lookupDeclaredStaticField(class, name); declared {
		if field != nil {
			return class, field
		}
		return nil, nil
	}
	visiting[class] = true
	defer delete(visiting, class)
	for _, parentName := range classParentNames(class) {
		parent := check.class(class.Owner, parentName)
		if owner, field := check.lookup(parent, name, visiting); owner != nil {
			return owner, field
		}
	}
	return nil, nil
}

func (check *staticFieldVisibility) inherits(class, owner *ClassDecl, seen map[*ClassDecl]bool) bool {
	if class == nil || seen[class] {
		return false
	}
	if class == owner {
		return true
	}
	seen[class] = true
	for _, parentName := range classParentNames(class) {
		if check.inherits(check.class(class.Owner, parentName), owner, seen) {
			return true
		}
	}
	return false
}

func (check *staticFieldVisibility) inaccessible(file *File, current *ClassDecl, qualifier, member string) *ClassDecl {
	visibility := memberVisibility(member)
	if visibility == memberPublic {
		return nil
	}
	receiver := check.class(file, qualifier)
	owner, _ := check.lookup(receiver, member, map[*ClassDecl]bool{})
	if owner == nil || visibility == memberPrivate && current == owner && receiver == owner ||
		visibility == memberProtected && check.inherits(current, owner, map[*ClassDecl]bool{}) {
		return nil
	}
	return owner
}

func (check *staticFieldVisibility) checkNativeSelector(declaration *GoDecl, selector *ast.SelectorExpr) error {
	if !check.enabled {
		return nil
	}
	qualifier := staticFieldQualifier(selector.X, nil)
	if owner := check.inaccessible(declaration.Owner, nil, qualifier, selector.Sel.Name); owner != nil {
		return goDeclMemberVisibilityError(declaration, selector.Sel.Pos(), selector.Sel.Name, owner)
	}
	return nil
}

func (check *staticFieldVisibility) validateSource(file *File, current *ClassDecl, walk func(classFieldVisitor, *fieldLocalScope)) error {
	scope := newFieldLocalScope(nil)
	pkg := check.model.Packages[file.Package]
	var problem error
	scope.qualifiedSelector = func(qualifier string, selector *SelectorExpr) {
		if problem != nil {
			return
		}
		if owner := check.inaccessible(file, current, qualifier, selector.Name); owner != nil {
			end := selector.Span().End
			problem = staticFieldVisibilityDiagnostic(file, sourceSpan(file.Source, end-len(selector.Name), end), owner, selector.Name)
		}
	}
	visitor := classFieldVisitor(func(use classFieldUse) {
		name := use.name.Name
		if problem != nil || current == nil || use.qualifier != "" || memberVisibility(name) != memberPrivate {
			return
		}
		_, imported := check.importsForFile(file)[name]
		if pkg.Values[name] || pkg.Functions[name] || pkg.Types[name] || pkg.Classes[name] != nil || imported || types.Universe.Lookup(name) != nil {
			return
		}
		owner, field := check.lookup(current, name, map[*ClassDecl]bool{})
		if owner == nil || owner == current || use.callee && !isFunctionType(field.TypeAST) {
			return
		}
		for _, method := range current.Methods {
			if method.Name == name {
				return
			}
		}
		problem = staticFieldVisibilityDiagnostic(file, use.name.Span(), owner, name)
	})
	walk(visitor, scope)
	return problem
}

func staticFieldVisibilityDiagnostic(file *File, span Span, owner *ClassDecl, name string) error {
	visibility := "private"
	if memberVisibility(name) == memberProtected {
		visibility = "protected"
	}
	filename := file.SourcePath
	if filename == "" {
		filename = file.Name
	}
	return diagnosticAt(filename, span, fmt.Errorf("cannot access %s member %s of class %s", visibility, name, owner.Name))
}

func (check *staticFieldVisibility) validateMethod(pkg *PackageSymbols, current *ClassDecl, method Method, implicitReceiverType string) error {
	if check.enabled && method.Owner != nil && method.BodyAST != nil {
		err := check.validateSource(method.Owner, current, func(visitor classFieldVisitor, scope *fieldLocalScope) {
			bindFieldLocals(scope, method.ParameterAST)
			bindFieldLocals(scope, method.ResultFieldsAST)
			for _, parameter := range method.TypeParamsAST {
				scope.names[parameter.Name] = true
			}
			if current != nil && !method.IsStatic {
				scope.names["this"] = true
				for _, parameter := range current.TypeParamsAST {
					scope.names[parameter.Name] = true
				}
			}
			if implicitReceiverType != "" {
				scope.names["this"] = true
			}
			visitor.block(method.BodyAST, scope)
		})
		if err != nil {
			return err
		}
	}
	return validateMemberVisibilityInMethod(check.model, pkg, current, method, implicitReceiverType)
}

func (check *staticFieldVisibility) validateInitializers(class *ClassDecl) error {
	if !check.enabled || class.Owner == nil {
		return nil
	}
	return check.validateSource(class.Owner, class, func(visitor classFieldVisitor, scope *fieldLocalScope) {
		walk := func(items []instanceInitializer) {
			for _, item := range items {
				if item.field != nil {
					visitor.expression(item.field.InitializerAST, scope, false)
				} else {
					visitor.block(item.block, scope)
				}
			}
		}
		walk(classInitializerOrder(class.StaticFields, class.StaticInitializers))
		scope.names["this"] = true
		for _, parameter := range class.TypeParamsAST {
			scope.names[parameter.Name] = true
		}
		walk(classInitializerOrder(class.Fields, class.InstanceInitializers))
	})
}
