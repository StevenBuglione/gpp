package javaport

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

const javaRecordType = "java.lang.Record"

func (program *javaObjectProgram) completeRecord(class *javaObjectClass) error {
	components := class.declaration.RecordComponents
	hasCanonical := slices.ContainsFunc(class.constructors, func(method *javaObjectMethod) bool {
		return slices.Equal(method.declaration.Parameters, components)
	})
	if !hasCanonical {
		return fmt.Errorf("missing attributed canonical record constructor")
	}
	for _, field := range class.fields {
		if !slices.Contains(field.Modifiers, "STATIC") && !slices.Contains(components, Parameter{Name: field.Name, Type: field.Type}) {
			return fmt.Errorf("missing attributed record component %s", field.Name)
		}
	}
	for _, component := range components {
		field, found := program.fields[class.declaration.Symbol+"#"+component.Name]
		if !found || field.declaration.Type != component.Type {
			return fmt.Errorf("missing attributed component field %s", component.Name)
		}
		if err := program.addImplicitRecordMethod(class, component.Name, component.Type, nil); err != nil {
			return err
		}
	}
	for _, method := range []struct {
		name, result string
		parameters   []Parameter
	}{
		{"equals", "boolean", []Parameter{{Name: "other", Type: javaObjectType}}},
		{"hashCode", "int", nil},
		{"toString", javaStringType, nil},
	} {
		if program.methods[recordMethodSymbol(class, method.name, method.parameters)] == nil {
			for _, component := range components {
				if !program.supportsRecordOperation(component.Type, method.name, method.result, method.parameters) {
					return fmt.Errorf("implicit record %s requires support for component %s of type %s", method.name, component.Name, component.Type)
				}
			}
		}
		if err := program.addImplicitRecordMethod(class, method.name, method.result, method.parameters); err != nil {
			return err
		}
	}
	return nil
}

func recordMethodSymbol(class *javaObjectClass, name string, parameters []Parameter) string {
	types := make([]string, len(parameters))
	for index, parameter := range parameters {
		types[index] = parameter.Type
	}
	return class.declaration.Symbol + "#" + name + "(" + strings.Join(types, ",") + ")"
}

func (program *javaObjectProgram) addImplicitRecordMethod(class *javaObjectClass, name, result string, parameters []Parameter) error {
	symbol := recordMethodSymbol(class, name, parameters)
	if program.methods[symbol] != nil {
		return nil
	}
	method := Occurrence{
		Kind: "METHOD", Name: name, Symbol: symbol, Owner: class.declaration.Symbol,
		Result: result, Parameters: parameters, Modifiers: []string{"PUBLIC", "FINAL"},
		Body: &MethodBody{}, Synthetic: true,
	}
	if err := program.addMethod(class, method); err != nil {
		return err
	}
	program.methods[symbol].recordImplicit = true
	return nil
}

func (program *javaObjectProgram) supportsRecordOperation(javaType, name, result string, parameters []Parameter) bool {
	if primitiveTypes[javaType] != "" || javaType == javaStringType || wrapperPrimitive(javaType) != "" ||
		javaType == javaNumberType || javaType == javaRecordType {
		return true
	}
	if _, rank := program.arrayType(javaType); rank != 0 {
		return true
	}
	class := program.classes[javaType]
	if class == nil {
		return false
	}
	if class.declaration.Kind == "RECORD" {
		return true
	}
	method := program.methods[recordMethodSymbol(class, name, parameters)]
	return method != nil && method.public && method.declaration.Result == result
}

func (ctx javaObjectContext) invokeRecordReference(expression *JavaExpression) (string, error) {
	var declaration Occurrence
	switch expression.Symbol {
	case javaRecordType + "#equals(java.lang.Object)":
		declaration = Occurrence{Name: "equals", Result: "boolean", Parameters: []Parameter{{Type: javaObjectType}}}
	case javaRecordType + "#hashCode()":
		declaration = Occurrence{Name: "hashCode", Result: "int"}
	case javaRecordType + "#toString()":
		declaration = Occurrence{Name: "toString", Result: javaStringType}
	default:
		return "", fmt.Errorf("unsupported Java Record method %s", expression.Symbol)
	}
	target, err := ctx.expression(expression.Target)
	if err != nil {
		return "", err
	}
	return ctx.emitReferenceInvocation(expression, declaration, target, func(receiver, arguments string) string {
		return receiver + "." + exportMethodName(declaration.Name) + "(" + arguments + ")"
	})
}

func (ctx javaObjectContext) emitRecordMethod(out *strings.Builder, method *javaObjectMethod) error {
	class := ctx.class
	name := class.storageName(class.goTypeName())
	declaration := method.declaration
	components := class.declaration.RecordComponents
	parameters, arguments := ctx.program.objectParameterList(declaration.Parameters)
	fmt.Fprintf(out, "\nfunc (this *%s) %s(%s)%s {\n", name, method.goName, parameters, ctx.program.objectResultType(method))
	switch declaration.Name {
	case "equals":
		ctx.program.runtime = true
		fmt.Fprintf(out, "\tother, sameType := javart.CanonicalReference(%s).(*%s)\n", arguments, name)
		out.WriteString("\tif !sameType { return false }\n\tif this == other { return true }\n")
		// OpenJDK's current record equalator visits components last-to-first.
		for _, component := range slices.Backward(components) {
			fmt.Fprintf(out, "\tif !javart.RecordComponentEqual(this.javaField%s, other.javaField%s) { return false }\n",
				component.Name, component.Name)
		}
		out.WriteString("\treturn true\n")
	case "hashCode":
		ctx.program.runtime = true
		// This is OpenJDK's algorithm, not a Java SE requirement.
		out.WriteString("\tvar hash int32\n")
		for _, component := range components {
			fmt.Fprintf(out, "\thash = 31*hash + javart.RecordComponentHash(this.javaField%s)\n", component.Name)
		}
		out.WriteString("\treturn hash\n")
	case "toString":
		ctx.program.runtime = true
		names := make([]string, 0, len(components))
		values := make([]string, 0, len(components))
		for _, component := range components {
			names = append(names, strconv.Quote(component.Name))
			values = append(values, "this.javaField"+component.Name)
		}
		fmt.Fprintf(out, "\treturn javart.RecordString(%q, []string{%s}", class.declaration.Name, strings.Join(names, ", "))
		if len(values) > 0 {
			fmt.Fprintf(out, ", %s", strings.Join(values, ", "))
		}
		out.WriteString(")\n")
	default:
		fmt.Fprintf(out, "\treturn this.javaField%s\n", declaration.Name)
	}
	out.WriteString("}\n")
	return nil
}
