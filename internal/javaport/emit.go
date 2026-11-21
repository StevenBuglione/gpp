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
	hasClass := false
	for _, file := range report.Files {
		for _, declaration := range file.Declarations {
			if declaration.Kind == "CLASS" && declaration.Owner == "" {
				hasClass = true
			}
			if declaration.Kind == "METHOD" && declaration.Name == "<init>" &&
				!slices.Contains(declaration.Modifiers, "PRIVATE") {
				return emitJavaObjects(report)
			}
		}
	}
	if hasClass {
		return emitPrimitiveUtilityClasses(report)
	}
	return EmitInterfaces(report)
}

// EmitInterfaces lowers the first supported source form: standalone, public
// Java interfaces with primitive signatures, integer or boolean constants,
// and same-package marker interfaces.
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
	fmt.Fprintf(&source, "package %s\n", packageName)
	var constants []string
	names := map[string]bool{}
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
				method, signature, err := primitiveInterfaceMethod(declaration)
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
	return source.String(), nil
}

func primitiveInterfaceMethod(declaration Occurrence) (string, string, error) {
	if !slices.Contains(declaration.Modifiers, "ABSTRACT") || declaration.HasTypeParams ||
		len(declaration.Thrown) != 0 {
		return "", "", fmt.Errorf("interface method requires abstract primitive signature without throws")
	}
	name := exportMethodName(declaration.Name)
	if !token.IsIdentifier(name) || !token.IsExported(name) {
		return "", "", fmt.Errorf("unsupported interface method name")
	}
	parts := make([]string, 0, len(declaration.Parameters))
	for _, parameter := range declaration.Parameters {
		goType, ok := primitiveTypes[parameter.Type]
		if !ok {
			return "", "", fmt.Errorf("unsupported interface parameter type %s", parameter.Type)
		}
		parts = append(parts, goType)
	}
	signature := "(" + strings.Join(parts, ", ") + ")"
	if declaration.Result != "void" {
		result, ok := primitiveTypes[declaration.Result]
		if !ok {
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
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Kind == "ERROR" {
			return "", fmt.Errorf("%s:%d:%d: %s", diagnostic.Path, diagnostic.Line, diagnostic.Column, diagnostic.Message)
		}
	}
	if len(report.Files) == 0 {
		return "", fmt.Errorf("no Java sources to emit")
	}
	packageName := report.Files[0].Package
	if packageName == "" || packageName == "main" {
		return "", fmt.Errorf("GPP emission requires a named Java package")
	}
	for _, part := range strings.Split(packageName, ".") {
		if !token.IsIdentifier(part) {
			return "", fmt.Errorf("cannot emit Java package %q as a GPP package", packageName)
		}
	}
	for _, file := range report.Files {
		if file.Package != packageName {
			return "", fmt.Errorf("%s: GPP emission currently requires one Java package", file.Path)
		}
	}
	return packageName, nil
}
