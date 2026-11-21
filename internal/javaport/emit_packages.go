package javaport

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// EmittedPackage maps one Java package to a GPP compilation unit.
type EmittedPackage struct {
	Name   string
	Source string
}

// EmitGPPPackages resolves a set of attributed Java sources before lowering
// each package. Logical imports are bound to the Go module during compilation.
func EmitGPPPackages(report Report) ([]EmittedPackage, error) {
	if err := emissionDiagnostics(report); err != nil {
		return nil, err
	}
	packages := map[string][]SourceFile{}
	module := report.Files[0].ModuleName
	for _, file := range report.Files {
		if file.Module != nil || file.ModuleName != module {
			return nil, fmt.Errorf("%s: batch emission of Java module descriptors or multiple Java modules is unsupported", file.Path)
		}
		if err := validateEmissionPackage(file.Package); err != nil {
			return nil, fmt.Errorf("%s: %w", file.Path, err)
		}
		packages[file.Package] = append(packages[file.Package], file)
	}
	names := slices.Sorted(maps.Keys(packages))
	var objectFiles []SourceFile
	objectPackages := map[string]bool{}
	for _, name := range names {
		if usesObjectEmitter(packages[name]) {
			objectFiles = append(objectFiles, packages[name]...)
			objectPackages[name] = true
		}
	}
	program, err := prepareJavaObjects(objectFiles)
	if err != nil {
		return nil, err
	}
	units := make([]EmittedPackage, 0, len(names))
	edges := make(map[PackageIdentity]map[PackageIdentity]bool, len(names))
	for _, name := range names {
		imports := map[PackageIdentity]bool{}
		edges[PackageIdentity{Package: name}] = imports
		if objectPackages[name] {
			unit, err := program.emitPackage(name)
			if err != nil {
				return nil, err
			}
			units = append(units, unit.EmittedPackage)
			for _, dependency := range unit.dependencies {
				imports[PackageIdentity{Package: dependency}] = true
			}
			continue
		}
		source, err := emitNonObjectPackage(Report{Files: packages[name]})
		if err != nil {
			return nil, err
		}
		units = append(units, EmittedPackage{Name: name, Source: source})
	}
	if cycles := findPackageCycles(edges); len(cycles) != 0 {
		var groups []string
		for _, cycle := range cycles {
			var members []string
			for _, pkg := range cycle {
				members = append(members, pkg.Package)
			}
			groups = append(groups, "["+strings.Join(members, ", ")+"]")
		}
		return nil, fmt.Errorf("Java package cycles require shared Go storage or dispatch boundaries: %s", strings.Join(groups, ", "))
	}
	return units, nil
}

type emittedObjectPackage struct {
	EmittedPackage
	dependencies []string
}

func (program javaObjectProgram) emitPackage(name string) (emittedObjectPackage, error) {
	program.pkg = name
	program.imports = map[string]string{}
	unit := emittedObjectPackage{EmittedPackage: EmittedPackage{Name: name}}

	var source strings.Builder
	for _, class := range program.layout {
		if class.file.Package != name {
			continue
		}
		if err := program.emitClass(&source, class); err != nil {
			return emittedObjectPackage{}, err
		}
	}
	var header strings.Builder
	fmt.Fprintf(&header, "package %s\n", name)
	if program.runtime {
		if name == "gpp.java" {
			return emittedObjectPackage{}, fmt.Errorf("Java runtime import conflicts with source package %q", name)
		}
		header.WriteString("\nimport javart \"gpp/java\"\n")
	}
	unit.dependencies = slices.Sorted(maps.Keys(program.imports))
	for _, dependency := range unit.dependencies {
		fmt.Fprintf(&header, "\nimport %s %s\n", program.imports[dependency], dependency)
	}
	unit.Source = header.String() + source.String()
	return unit, nil
}

func (program *javaObjectProgram) qualify(pkg, name string) string {
	if pkg == program.pkg {
		return name
	}
	alias := program.imports[pkg]
	if alias == "" {
		alias = fmt.Sprintf("javapkg%d", len(program.imports))
		program.imports[pkg] = alias
	}
	return alias + "." + name
}
