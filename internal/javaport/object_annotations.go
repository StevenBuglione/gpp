package javaport

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

type javaAnnotationType struct {
	file           SourceFile
	declaration    Occurrence
	members        []Occurrence
	storagePackage string
	symbolPrefix   string
}

func annotationTypeAPI(name string) string    { return "GppJavaAnnotationType_" + name }
func annotationElementAPI(name string) string { return "GppJavaAnnotations_" + name }

func (annotation *javaAnnotationType) goName() string {
	return annotation.symbolPrefix + annotationTypeAPI(annotation.declaration.Name)
}

func hasRuntimeAnnotations(annotations []Annotation) bool {
	return slices.ContainsFunc(annotations, func(annotation Annotation) bool { return annotation.Retention == "RUNTIME" })
}

func declarationHasRuntimeAnnotations(declaration Occurrence) bool {
	return hasRuntimeAnnotations(declaration.Annotations) || slices.ContainsFunc(declaration.ParameterAnnotations, hasRuntimeAnnotations)
}

func (program *javaObjectProgram) emitsClassAnnotations(class *javaObjectClass) bool {
	for owner := class; owner != nil; owner = owner.parent {
		if declarationHasRuntimeAnnotations(owner.declaration) || len(program.annotationMembers[owner.declaration.Symbol]) != 0 {
			return true
		}
	}
	return false
}

func (program *javaObjectProgram) emitsInterfaceAnnotations(iface *javaInterface) bool {
	return declarationHasRuntimeAnnotations(iface.declaration) || len(program.annotationMembers[iface.declaration.Symbol]) != 0
}

func (program *javaObjectProgram) reserveAnnotationExports() error {
	reserve := func(file SourceFile, declaration Occurrence, name string) error {
		if program.exports[file.Package][name] {
			return unsupportedSource(file, declaration, "duplicate Go annotation accessor "+name)
		}
		program.exports[file.Package][name] = true
		return nil
	}
	for _, symbol := range slices.Sorted(maps.Keys(program.annotationTypes)) {
		annotation := program.annotationTypes[symbol]
		if err := reserve(annotation.file, annotation.declaration, annotation.goName()); err != nil {
			return err
		}
	}
	for _, class := range program.layout {
		if program.emitsClassAnnotations(class) {
			if err := reserve(class.file, class.declaration, annotationElementAPI(class.goTypeName())); err != nil {
				return err
			}
		}
	}
	for _, iface := range program.interfaceLayout {
		if program.emitsInterfaceAnnotations(iface) {
			if err := reserve(iface.file, iface.declaration, annotationElementAPI(iface.declaration.Name)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (program *javaObjectProgram) annotationTypeReference(annotation *javaAnnotationType) string {
	if annotation.storagePackage == program.pkg || annotation.storagePackage == program.facadeOf {
		return program.qualify(annotation.storagePackage, annotation.goName())
	}
	return program.qualify(annotation.file.Package, annotationTypeAPI(annotation.declaration.Name))
}

func (program *javaObjectProgram) emitAnnotationMetadata(out *strings.Builder) error {
	for _, symbol := range slices.Sorted(maps.Keys(program.annotationTypes)) {
		annotation := program.annotationTypes[symbol]
		if annotation.storagePackage == program.pkg {
			if err := program.emitAnnotationType(out, annotation); err != nil {
				return unsupportedSource(annotation.file, annotation.declaration, err.Error())
			}
		}
	}
	for _, class := range program.layout {
		if class.storagePackage != program.pkg || !program.emitsClassAnnotations(class) {
			continue
		}
		parent := "nil"
		if class.parent != nil && program.emitsClassAnnotations(class.parent) {
			parent = program.classReference(class.parent, annotationElementAPI(class.parent.goTypeName()))
		}
		if err := program.emitAnnotatedElement(out, class.storageName(annotationElementAPI(class.goTypeName())), class.declaration, parent); err != nil {
			return unsupportedSource(class.file, class.declaration, err.Error())
		}
	}
	for _, iface := range program.interfaceLayout {
		if iface.storagePackage == program.pkg && program.emitsInterfaceAnnotations(iface) {
			if err := program.emitAnnotatedElement(out, iface.storageName(annotationElementAPI(iface.declaration.Name)), iface.declaration, "nil"); err != nil {
				return unsupportedSource(iface.file, iface.declaration, err.Error())
			}
		}
	}
	return nil
}

func (program *javaObjectProgram) emitAnnotationFacade(out *strings.Builder, name, storage string) {
	forward := func(name, kind, target string) {
		program.runtime = true
		fmt.Fprintf(out, "\nfunc %s() javart.%s { return %s() }\n", name, kind, program.qualify(storage, target))
	}
	for _, symbol := range slices.Sorted(maps.Keys(program.annotationTypes)) {
		annotation := program.annotationTypes[symbol]
		if annotation.file.Package == name {
			forward(annotationTypeAPI(annotation.declaration.Name), "AnnotationType", annotation.goName())
		}
	}
	for _, class := range program.layout {
		if class.file.Package == name && program.emitsClassAnnotations(class) {
			api := annotationElementAPI(class.goTypeName())
			forward(api, "AnnotatedElement", class.storageName(api))
		}
	}
	for _, iface := range program.interfaceLayout {
		if iface.file.Package == name && program.emitsInterfaceAnnotations(iface) {
			api := annotationElementAPI(iface.declaration.Name)
			forward(api, "AnnotatedElement", iface.storageName(api))
		}
	}
}

func (program *javaObjectProgram) emitAnnotationType(out *strings.Builder, annotation *javaAnnotationType) error {
	declaration := annotation.declaration
	retention, container, inherited := "CLASS", "", false
	for _, meta := range declaration.Annotations {
		switch meta.Type {
		case "java.lang.annotation.Retention":
			if len(meta.Members) == 1 {
				_, retention, _ = strings.Cut(meta.Members[0].Value.Text, "#")
			}
		case "java.lang.annotation.Repeatable":
			if len(meta.Members) == 1 {
				container = meta.Members[0].Value.Text
			}
		case "java.lang.annotation.Inherited":
			inherited = true
		}
	}

	var members []string
	for _, member := range annotation.members {
		value := "nil"
		if member.AnnotationDefault != nil {
			var err error
			value, err = program.annotationValue(*member.AnnotationDefault)
			if err != nil {
				return fmt.Errorf("annotation member %s: %w", member.Name, err)
			}
		}
		members = append(members, fmt.Sprintf("javart.NewAnnotationMember(%q, %q, %s, false, %t)",
			member.Name, member.Result, value, member.AnnotationDefault != nil))
	}
	metadata, err := program.annotationElement(declaration.Annotations, "nil", "nil")
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "\nfunc %s() javart.AnnotationType {\n\treturn javart.NewAnnotationType(%q, %q, %q, %t, %s, %s)\n}\n",
		annotation.goName(), declaration.Symbol, retention, container, inherited, annotationSlice("AnnotationMember", members), metadata)
	return nil
}

func (program *javaObjectProgram) emitAnnotatedElement(out *strings.Builder, name string, declaration Occurrence, parent string) error {
	var members []string
	for _, member := range program.annotationMembers[declaration.Symbol] {
		target, err := program.annotationElement(member.Annotations, "nil", "nil")
		if err != nil {
			return fmt.Errorf("annotation on %s: %w", member.Symbol, err)
		}
		var parameters []string
		for index := range member.Parameters {
			var annotations []Annotation
			if index < len(member.ParameterAnnotations) {
				annotations = member.ParameterAnnotations[index]
			}
			metadata, err := program.annotationElement(annotations, "nil", "nil")
			if err != nil {
				return fmt.Errorf("parameter annotation on %s: %w", member.Symbol, err)
			}
			parameters = append(parameters, metadata)
		}
		members = append(members, fmt.Sprintf("javart.NewAnnotatedMember(%q, %s, %s)",
			member.Symbol, target, annotationSlice("AnnotatedElement", parameters)))
	}
	metadata, err := program.annotationElement(declaration.Annotations, parent, annotationSlice("AnnotatedMember", members))
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "\nfunc %s() javart.AnnotatedElement {\n\treturn %s\n}\n", name, metadata)
	return nil
}

func (program *javaObjectProgram) annotationElement(annotations []Annotation, parent, members string) (string, error) {
	program.runtime = true
	var entries []string
	for _, annotation := range annotations {
		if annotation.Retention != "RUNTIME" {
			continue
		}
		entry, err := program.annotationLiteral(annotation)
		if err != nil {
			return "", err
		}
		entries = append(entries, entry)
	}
	return fmt.Sprintf("javart.NewAnnotatedElement(%s, %s, %s)", annotationSlice("Annotation", entries), parent, members), nil
}

func (program *javaObjectProgram) annotationLiteral(annotation Annotation) (string, error) {
	if annotation.Type == "" {
		return "", fmt.Errorf("missing attributed annotation type")
	}
	var members []string
	for _, member := range annotation.Members {
		value, err := program.annotationValue(member.Value)
		if err != nil {
			return "", fmt.Errorf("annotation %s element %s: %w", annotation.Type, member.Name, err)
		}
		members = append(members, fmt.Sprintf("javart.NewAnnotationMember(%q, %q, %s, %t, true)",
			member.Name, member.Type, value, member.Explicit))
	}
	declaration := "nil"
	if annotationType := program.annotationTypes[annotation.Type]; annotationType != nil {
		declaration = program.annotationTypeReference(annotationType)
	}
	return fmt.Sprintf("javart.NewAnnotation(%q, %s, %s)", annotation.Type, annotationSlice("AnnotationMember", members), declaration), nil
}

func (program *javaObjectProgram) annotationValue(value AnnotationValue) (string, error) {
	switch value.Kind {
	case "STRING":
		if value.UTF16LE == nil {
			return "javart.StringFromGo(" + strconv.Quote(value.Text) + ")", nil
		}
		return program.stringLiteral(value.UTF16LE)
	case "CLASS_LITERAL":
		if value.Text != "" {
			return "javart.AnnotationClassLiteral(" + strconv.Quote(value.Text) + ")", nil
		}
	case "ENUM_CONSTANT":
		if owner, name, ok := strings.Cut(value.Text, "#"); ok && owner != "" && name != "" {
			return fmt.Sprintf("javart.AnnotationEnumConstant{Type: %q, Name: %q}", owner, name), nil
		}
	case "ANNOTATION":
		if value.Annotation != nil {
			return program.annotationLiteral(*value.Annotation)
		}
	case "ARRAY":
		elements := make([]string, 0, len(value.Elements))
		for _, element := range value.Elements {
			text, err := program.annotationValue(element)
			if err != nil {
				return "", err
			}
			elements = append(elements, text)
		}
		return "[]any{" + strings.Join(elements, ", ") + "}", nil
	case "INTEGER":
		return program.primitiveLowering.literal("int", value.Text)
	default:
		javaType := strings.ToLower(value.Kind)
		if primitiveTypes[javaType] != "" {
			return program.primitiveLowering.literal(javaType, value.Text)
		}
	}
	return "", fmt.Errorf("unsupported attributed Java annotation value %s", value.Kind)
}

func annotationSlice(kind string, entries []string) string {
	if len(entries) == 0 {
		return "nil"
	}
	return "[]javart." + kind + "{" + strings.Join(entries, ", ") + "}"
}
