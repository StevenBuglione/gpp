package javaport

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

func javaIsSealed(declaration Occurrence) bool {
	return slices.Contains(declaration.Modifiers, "SEALED")
}

func (class *javaObjectClass) isAbstract() bool {
	return slices.Contains(class.declaration.Modifiers, "ABSTRACT")
}

// Validate the emitted closed-world hierarchy even when a caller supplies an
// attributed Report directly. Javac independently checks these relationships
// and resolves the inferred permits list for normal source input.
func (program *javaObjectProgram) validateSealedTypes() error {
	type attributedType struct {
		file SourceFile
		decl Occurrence
	}
	types := map[string]attributedType{}
	for symbol, class := range program.classes {
		types[symbol] = attributedType{class.file, class.declaration}
	}
	for symbol, iface := range program.interfaces {
		types[symbol] = attributedType{iface.file, iface.declaration}
	}
	for _, symbol := range slices.Sorted(maps.Keys(types)) {
		owner := types[symbol]
		sealed, nonSealed, final := javaIsSealed(owner.decl), slices.Contains(owner.decl.Modifiers, "NON_SEALED"),
			slices.Contains(owner.decl.Modifiers, "FINAL")
		if sealed && (nonSealed || final) || nonSealed && final {
			return unsupportedSource(owner.file, owner.decl, "conflicting Java inheritance modifiers")
		}
		if sealed {
			if len(owner.decl.PermittedSubclasses) == 0 {
				return unsupportedSource(owner.file, owner.decl, "missing attributed Java permits list")
			}
			seen := map[string]bool{}
			for _, name := range owner.decl.PermittedSubclasses {
				child, found := types[name]
				if seen[name] {
					return unsupportedSource(owner.file, owner.decl, "duplicate Java permitted subtype "+name)
				}
				seen[name] = true
				if !found {
					return unsupportedSource(owner.file, owner.decl, "permitted Java subtype is outside this emission: "+name)
				}
				if !slices.Contains(child.decl.Supertypes, symbol) {
					return unsupportedSource(owner.file, owner.decl, name+" is not a direct subtype")
				}
				if owner.decl.ModuleName != child.decl.ModuleName ||
					(owner.decl.ModuleName == "" && owner.file.Package != child.file.Package) {
					return unsupportedSource(owner.file, owner.decl, name+" is outside the Java sealed type's module or unnamed-module package")
				}
			}
			name := javaPermitsAPI(owner.decl.Name)
			if program.exports[owner.file.Package][name] {
				return unsupportedSource(owner.file, owner.decl, "duplicate Go permits accessor "+name)
			}
			program.exports[owner.file.Package][name] = true
		} else if len(owner.decl.PermittedSubclasses) != 0 {
			return unsupportedSource(owner.file, owner.decl, "Java permits require a sealed type")
		}
		sealedParent := false
		for _, parentName := range owner.decl.Supertypes {
			parent, found := types[parentName]
			if !found {
				continue
			}
			if slices.Contains(parent.decl.Modifiers, "FINAL") {
				return unsupportedSource(owner.file, owner.decl, "cannot extend final Java type "+parentName)
			}
			if !javaIsSealed(parent.decl) {
				continue
			}
			sealedParent = true
			if !slices.Contains(parent.decl.PermittedSubclasses, symbol) {
				return unsupportedSource(owner.file, owner.decl, "subtype is not permitted by sealed Java type "+parentName)
			}
			if !sealed && !nonSealed && !final {
				return unsupportedSource(owner.file, owner.decl, "direct subtype of a sealed Java type must be final, sealed, or non-sealed")
			}
		}
		if nonSealed && !sealedParent {
			return unsupportedSource(owner.file, owner.decl, "non-sealed Java type requires a direct sealed parent")
		}
	}
	return nil
}

func javaPermitsAPI(name string) string {
	return "GppJavaPermittedSubclasses_" + name
}

func emitJavaPermits(out *strings.Builder, declaration Occurrence, name string) {
	if !javaIsSealed(declaration) {
		return
	}
	entries := make([]string, len(declaration.PermittedSubclasses))
	for index, entry := range declaration.PermittedSubclasses {
		entries[index] = strconv.Quote(entry)
	}
	fmt.Fprintf(out, "\nfunc %s() []string { return []string{%s} }\n", name, strings.Join(entries, ", "))
}

func (iface *javaInterface) sealedMarker() string {
	return fmt.Sprintf("GppJavaSealed_%x", iface.declaration.Symbol)
}
