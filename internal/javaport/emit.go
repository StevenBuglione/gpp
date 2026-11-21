package javaport

import (
	"fmt"
	"go/token"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var primitiveTypes = map[string]string{
	"boolean": "bool",
	"byte":    "int8",
	"short":   "int16",
	"char":    "uint16",
	"int":     "int32",
	"long":    "int64",
	"float":   "float32",
	"double":  "float64",
}

// EmitGPP translates supported Java declaration and executable forms.
func EmitGPP(report Report) (string, error) {
	if usesObjectEmitter(report.Files) {
		return emitJavaObjects(report)
	}
	return emitNonObjectPackage(report)
}

func usesObjectEmitter(files []SourceFile) bool {
	for _, file := range files {
		for _, declaration := range file.Declarations {
			if declaration.Owner == "" && declaration.Kind == "INTERFACE" {
				return true
			}
			if declaration.Kind == "BLOCK" && declaration.StaticInitializer ||
				declaration.Kind == "VARIABLE" && slices.Contains(declaration.Modifiers, "STATIC") {
				return true
			}
			if declaration.Kind == "METHOD" && declaration.Name == "<init>" &&
				!slices.Contains(declaration.Modifiers, "PRIVATE") {
				return true
			}
		}
	}
	return false
}

func emitNonObjectPackage(report Report) (string, error) {
	hasClass := false
	for _, file := range report.Files {
		for _, declaration := range file.Declarations {
			if declaration.Kind == "CLASS" && declaration.Owner == "" {
				hasClass = true
			}
		}
	}
	if hasClass {
		return emitPrimitiveUtilityClasses(report)
	}
	return EmitInterfaces(report)
}

// EmitInterfaces lowers the first supported source form: standalone, public
// Java interfaces with primitive and boxed signatures, integer or boolean
// constants, and same-package marker interfaces.
func EmitInterfaces(report Report) (string, error) {
	packageName, err := emissionPackage(report)
	if err != nil {
		return "", err
	}

	interfaceNames := map[string]string{}
	javaMethodNames := map[string]bool{}
	for _, file := range report.Files {
		for _, declaration := range file.Declarations {
			if declaration.Kind == "INTERFACE" && declaration.Owner == "" {
				interfaceNames[declaration.Symbol] = declaration.Name
			}
			if declaration.Kind == "METHOD" {
				javaMethodNames[exportMethodName(declaration.Name)] = true
			}
		}
	}

	var source strings.Builder
	var constants []string
	names := map[string]bool{}
	lowering := primitiveLowering{}
	for _, file := range report.Files {
		if len(file.Calls) > 0 {
			return "", unsupportedSource(file, file.Calls[0], "calls inside standalone interface declarations")
		}
		owner := ""
		var interfaceName string
		var methods map[string]bool
		for _, declaration := range file.Declarations {
			if declaration.Kind == "INTERFACE" && declaration.Owner == "" {
				if !slices.Contains(declaration.Modifiers, "PUBLIC") || declaration.HasTypeParams ||
					declaration.AnnotationCount != 0 ||
					!token.IsIdentifier(declaration.Name) || !token.IsExported(declaration.Name) ||
					slices.Contains(declaration.Modifiers, "SEALED") || slices.Contains(declaration.Modifiers, "NON_SEALED") {
					return "", unsupportedSource(file, declaration, "interface modifiers, annotations, generics or name")
				}
				if names[declaration.Name] {
					return "", unsupportedSource(file, declaration, "duplicate exported GPP name")
				}
				if owner != "" {
					source.WriteString("}\n")
				}
				owner = declaration.Symbol
				interfaceName = declaration.Name
				names[interfaceName] = true
				methods = map[string]bool{}
				fmt.Fprintf(&source, "\ntype %s interface {\n", interfaceName)
				if len(declaration.Supertypes) > 0 {
					marker := "Is" + interfaceName
					if javaMethodNames[marker] {
						return "", unsupportedSource(file, declaration, "marker method collides with a Java method")
					}
					for _, superclass := range declaration.Supertypes {
						parent, ok := interfaceNames[superclass]
						if !ok {
							return "", unsupportedSource(file, declaration, "superinterface must be in the same emission")
						}
						fmt.Fprintf(&source, "\t%s\n", parent)
					}
					fmt.Fprintf(&source, "\t%s()\n", marker)
				}
				continue
			}
			if owner == "" || declaration.Owner != owner || declaration.AnnotationCount != 0 {
				return "", unsupportedSource(file, declaration, "nested, annotated, or non-interface declaration")
			}
			switch declaration.Kind {
			case "METHOD":
				method, signature, err := lowering.emitInterfaceMethod(declaration)
				if err != nil {
					return "", unsupportedSource(file, declaration, err.Error())
				}
				if methods[method] {
					return "", unsupportedSource(file, declaration, "overloaded or colliding interface method")
				}
				methods[method] = true
				fmt.Fprintf(&source, "\t%s%s\n", method, signature)
			case "VARIABLE":
				goType, ok := primitiveTypes[declaration.Type]
				if !ok || declaration.Constant == nil || !token.IsIdentifier(declaration.Name) ||
					declaration.Type == "float" || declaration.Type == "double" {
					return "", unsupportedSource(file, declaration, "non-constant or unsupported interface field")
				}
				if declaration.Type != "boolean" {
					if _, err := strconv.ParseInt(*declaration.Constant, 10, 64); err != nil {
						return "", unsupportedSource(file, declaration, "unsupported constant value")
					}
				}
				name := interfaceName + declaration.Name
				if names[name] {
					return "", unsupportedSource(file, declaration, "duplicate exported GPP name")
				}
				names[name] = true
				constants = append(constants, fmt.Sprintf("const %s %s = %s\n", name, goType, *declaration.Constant))
			default:
				return "", unsupportedSource(file, declaration, "unsupported interface member")
			}
		}
		if owner != "" {
			source.WriteString("}\n")
		}
	}
	if len(names) == 0 {
		return "", fmt.Errorf("no Java interfaces to emit")
	}
	for _, constant := range constants {
		source.WriteByte('\n')
		source.WriteString(constant)
	}
	header := "package " + packageName + "\n"
	if lowering.javaRuntime {
		if packageName == "gpp.java" {
			return "", fmt.Errorf("Java runtime import conflicts with source package %q", packageName)
		}
		header += "\nimport javart \"gpp/java\"\n"
	}
	return header + source.String(), nil
}

func (lowering *primitiveLowering) emitInterfaceMethod(declaration Occurrence) (string, string, error) {
	if !slices.Contains(declaration.Modifiers, "ABSTRACT") || declaration.HasTypeParams ||
		len(declaration.Thrown) != 0 {
		return "", "", fmt.Errorf("interface method requires an abstract signature without throws")
	}
	name := exportMethodName(declaration.Name)
	if !token.IsIdentifier(name) || !token.IsExported(name) {
		return "", "", fmt.Errorf("unsupported interface method name")
	}
	parts := make([]string, 0, len(declaration.Parameters))
	for _, parameter := range declaration.Parameters {
		goType := lowering.signatureType(parameter.Type)
		if goType == "" {
			return "", "", fmt.Errorf("unsupported interface parameter type %s", parameter.Type)
		}
		parts = append(parts, goType)
	}
	signature := "(" + strings.Join(parts, ", ") + ")"
	if declaration.Result != "void" {
		result := lowering.signatureType(declaration.Result)
		if result == "" {
			return "", "", fmt.Errorf("unsupported interface result type %s", declaration.Result)
		}
		signature += " " + result
	}
	return name, signature, nil
}

func exportMethodName(name string) string {
	first, size := utf8.DecodeRuneInString(name)
	return string(unicode.ToUpper(first)) + name[size:]
}

func unsupportedSource(file SourceFile, occurrence Occurrence, problem string) error {
	return fmt.Errorf("%s:%d:%d: cannot emit %s: %s", file.Path, occurrence.Line,
		occurrence.Column, occurrence.Symbol, problem)
}

func emissionPackage(report Report) (string, error) {
	if err := emissionDiagnostics(report); err != nil {
		return "", err
	}
	packageName := report.Files[0].Package
	if err := validateEmissionPackage(packageName); err != nil {
		return "", err
	}
	for _, file := range report.Files {
		if file.Package != packageName {
			return "", fmt.Errorf("%s: use --out-dir to emit multiple Java packages", file.Path)
		}
	}
	return packageName, nil
}

func emissionDiagnostics(report Report) error {
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Kind == "ERROR" {
			return fmt.Errorf("%s:%d:%d: %s", diagnostic.Path, diagnostic.Line, diagnostic.Column, diagnostic.Message)
		}
	}
	if len(report.Files) == 0 {
		return fmt.Errorf("no Java sources to emit")
	}
	return nil
}

func validateEmissionPackage(packageName string) error {
	if packageName == "" {
		return fmt.Errorf("GPP emission requires a named Java package")
	}
	parts := strings.Split(packageName, ".")
	for _, part := range parts {
		if !token.IsIdentifier(part) {
			return fmt.Errorf("cannot emit Java package %q as a GPP package", packageName)
		}
	}
	if parts[len(parts)-1] == "main" || parts[len(parts)-1] == "_" {
		return fmt.Errorf("Java package %q maps to a Go package name that cannot be imported", packageName)
	}
	return nil
}
