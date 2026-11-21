package javaport

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// EmittedPackage describes a named GPP compilation unit from Java migration.
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
	units := make(map[string]EmittedPackage, len(names))
	edges := make(map[PackageIdentity]map[PackageIdentity]bool, len(names))
	for _, name := range names {
		imports := map[PackageIdentity]bool{}
		edges[PackageIdentity{Package: name}] = imports
		if objectPackages[name] {
			unit, err := program.emitPackage(name)
			if err != nil {
				return nil, err
			}
			units[name] = unit.EmittedPackage
			for _, dependency := range unit.dependencies {
				imports[PackageIdentity{Package: dependency}] = true
			}
			continue
		}
		source, err := emitNonObjectPackage(Report{Files: packages[name]})
		if err != nil {
			return nil, err
		}
		units[name] = EmittedPackage{Name: name, Source: source}
	}
	if err := program.emitPackageCycles(units, findPackageCycles(edges)); err != nil {
		return nil, err
	}
	result := make([]EmittedPackage, 0, len(units))
	for _, name := range slices.Sorted(maps.Keys(units)) {
		result = append(result, units[name])
	}
	return result, nil
}

type emittedObjectPackage struct {
	EmittedPackage
	dependencies []string
}

func (program javaObjectProgram) emitPackage(name string) (emittedObjectPackage, error) {
	program.pkg = name
	program.imports = map[string]string{}
	var source strings.Builder
	for _, class := range program.layout {
		if class.storagePackage != name {
			continue
		}
		if err := program.emitClass(&source, class); err != nil {
			return emittedObjectPackage{}, err
		}
	}
	return program.finishPackage(&source)
}

func (program *javaObjectProgram) finishPackage(source *strings.Builder) (emittedObjectPackage, error) {
	unit := emittedObjectPackage{EmittedPackage: EmittedPackage{Name: program.pkg}}
	var header strings.Builder
	fmt.Fprintf(&header, "package %s\n", program.pkg)
	if program.runtime || program.primitiveLowering.javaRuntime {
		if program.pkg == "gpp.java" {
			return emittedObjectPackage{}, fmt.Errorf("Java runtime import conflicts with source package %q", program.pkg)
		}
		header.WriteString("\nimport javart \"gpp/java\"\n")
	}
	program.primitiveLowering.emitNativeImports(&header)
	unit.dependencies = slices.Sorted(maps.Keys(program.imports))
	for _, dependency := range unit.dependencies {
		fmt.Fprintf(&header, "\nimport %s %s\n", program.imports[dependency], dependency)
	}
	unit.Source = header.String() + source.String()
	return unit, nil
}

func (program *javaObjectProgram) classReference(class *javaObjectClass, name string) string {
	if program.facadeOf != "" && class.file.Package == program.pkg {
		return name
	}
	if class.storagePackage == program.pkg || class.storagePackage == program.facadeOf {
		return program.qualify(class.storagePackage, class.storageName(name))
	}
	return program.qualify(class.file.Package, name)
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
