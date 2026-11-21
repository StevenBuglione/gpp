package compiler

import "strings"

func validateClassSealing(program *Program, model *SemanticModel, lookupClass func(*File, string) *ClassDecl) []error {
	var classes []*ClassDecl
	hasModifiers := false
	for _, file := range program.Files {
		for _, decl := range file.Decls {
			if class, ok := decl.(*ClassDecl); ok {
				classes = append(classes, class)
				hasModifiers = hasModifiers || class.InheritanceModifier != ""
			}
		}
	}
	if !hasModifiers {
		return nil
	}

	parents := map[*ClassDecl][]*ClassDecl{}
	children := map[*ClassDecl]map[*ClassDecl]bool{}
	for _, class := range classes {
		for _, name := range class.ParentNames() {
			if parent := lookupClass(class.Owner, name); parent != nil {
				parents[class] = append(parents[class], parent)
				if children[parent] == nil {
					children[parent] = map[*ClassDecl]bool{}
				}
				children[parent][class] = true
			}
		}
	}

	var diagnostics []error
	permitted := map[*ClassDecl]map[*ClassDecl]bool{}
	for _, class := range classes {
		if class.InheritanceModifier != "sealed" {
			continue
		}
		allowed := map[*ClassDecl]bool{}
		permitted[class] = allowed
		names := class.PermittedNames()
		if len(names) == 0 {
			for child := range children[class] {
				if child.Owner == class.Owner {
					allowed[child] = true
				}
			}
			if len(allowed) == 0 {
				diagnostics = append(diagnostics, classDiagnosticf(class, "sealed class %s must permit at least one direct subclass", class.Name))
			}
			continue
		}
		for _, name := range names {
			child := lookupClass(class.Owner, name)
			if child == nil {
				// A logical package name can refer to a child without introducing
				// a Go import back from its ancestor's package to the child.
				if dot := strings.LastIndexByte(name, '.'); dot >= 0 {
					if pkg := model.Packages[name[:dot]]; pkg != nil {
						child = pkg.Classes[name[dot+1:]]
					}
				}
			}
			switch {
			case child == nil:
				diagnostics = append(diagnostics, classDiagnosticf(class, "unknown permitted subclass %s of sealed class %s", name, class.Name))
			case !children[class][child]:
				diagnostics = append(diagnostics, classDiagnosticf(class, "permitted class %s is not a direct subclass of %s", name, class.Name))
			case allowed[child]:
				diagnostics = append(diagnostics, classDiagnosticf(class, "duplicate permitted subclass %s of sealed class %s", name, class.Name))
			default:
				allowed[child] = true
			}
		}
	}

	for _, class := range classes {
		hasSealedParent := false
		for _, parent := range parents[class] {
			switch parent.InheritanceModifier {
			case "final":
				diagnostics = append(diagnostics, classDiagnosticf(class, "class %s cannot extend final class %s", class.Name, parent.Name))
			case "sealed":
				hasSealedParent = true
				if !permitted[parent][class] {
					diagnostics = append(diagnostics, classDiagnosticf(class, "class %s is not permitted by sealed class %s", class.Name, parent.Name))
				} else if class.InheritanceModifier == "" {
					diagnostics = append(diagnostics, classDiagnosticf(class, "direct subclass %s of sealed class %s must be final, sealed, or non-sealed", class.Name, parent.Name))
				}
			}
		}
		if class.InheritanceModifier == "non-sealed" && !hasSealedParent {
			diagnostics = append(diagnostics, classDiagnosticf(class, "non-sealed class %s requires a direct sealed parent", class.Name))
		}
	}
	return diagnostics
}
