package javaport

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"unicode/utf16"
)

const javaEnumType = "java.lang.Enum"

func (class *javaObjectClass) isEnum() bool {
	return class.declaration.Kind == "ENUM" && class.enumBase == nil
}

func (class *javaObjectClass) hasEnumIdentity() bool {
	return class.declaration.Kind == "ENUM"
}

func (program *javaObjectProgram) enumVariantBase(declaration Occurrence) *javaObjectClass {
	if len(declaration.Supertypes) != 1 {
		return nil
	}
	base := program.classes[declaration.Supertypes[0]]
	if base == nil || !base.isEnum() ||
		!strings.HasPrefix(declaration.Symbol, base.declaration.Symbol+"$") ||
		!slices.Contains(base.declaration.PermittedSubclasses, declaration.Symbol) {
		return nil
	}
	return base
}

func (class *javaObjectClass) enumValuesStorage() string {
	return class.storageName("javaEnumValues_" + class.declaration.Name)
}

func (program *javaObjectProgram) addEnumConstant(class *javaObjectClass, declaration Occurrence) error {
	if !class.isEnum() || declaration.Type != class.declaration.Symbol ||
		declaration.Initializer == nil || declaration.Initializer.Kind != "NEW_CLASS" {
		return fmt.Errorf("missing attributed Java enum constant %s", declaration.Name)
	}
	field := declaration
	field.Initializer = nil
	if err := program.addStaticField(class, field); err != nil {
		return err
	}
	class.enumConstants = append(class.enumConstants, declaration)
	return nil
}

func (program *javaObjectProgram) completeEnum(class *javaObjectClass) error {
	owner := class.declaration.Symbol
	for _, method := range []Occurrence{
		{Name: "values", Symbol: owner + "#values()", Result: owner + "[]"},
		{Name: "valueOf", Symbol: owner + "#valueOf(java.lang.String)", Result: owner,
			Parameters: []Parameter{{Name: "name", Type: javaStringType}}},
	} {
		method.Kind, method.Owner = "METHOD", owner
		method.Modifiers = []string{"PUBLIC", "STATIC"}
		method.Synthetic, method.Body = true, &MethodBody{}
		if err := program.addStaticMethod(class, method); err != nil {
			return err
		}
		program.methods[method.Symbol].enumImplicit = true
	}
	symbol := owner + "#toString()"
	if program.methods[symbol] == nil {
		method := Occurrence{Kind: "METHOD", Name: "toString", Owner: owner, Symbol: symbol, Result: javaStringType,
			Modifiers: []string{"PUBLIC"}, Synthetic: true, Body: &MethodBody{}}
		if err := program.addMethod(class, method); err != nil {
			return err
		}
		program.methods[symbol].enumImplicit = true
	}
	return nil
}

func (program *javaObjectProgram) emitEnumMembers(out *strings.Builder, class *javaObjectClass, name string) {
	fmt.Fprintf(out, "\nfunc (this *%s) Name() *javart.String { return this.javaEnumName }\n", name)
	fmt.Fprintf(out, "\nfunc (this *%s) Ordinal() int32 { return this.javaEnumOrdinal }\n", name)
	fmt.Fprintf(out, "\nfunc (*%s) GppJavaEnum() string { return %q }\n", name, class.declaration.Symbol)

}

func enumNameArgument(name string) string {
	var data []byte
	for _, codeUnit := range utf16.Encode([]rune(name)) {
		data = binary.LittleEndian.AppendUint16(data, codeUnit)
	}
	return fmt.Sprintf("javart.InternStringLiteral(%q)", string(data))
}

func enumConstantGoName(name string) string {
	const prefix = "GppEnumName_"
	if !strings.HasPrefix(name, prefix) && strings.IndexFunc(name, func(ch rune) bool {
		return !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_')
	}) == -1 {
		return name
	}
	// The GPP lexer requires ASCII identifiers. Preserve the Java spelling
	// in Name/valueOf and use an injective Go bridge name for the field.
	return prefix + hex.EncodeToString([]byte(name))
}

func (ctx javaObjectContext) enumSwitchMatch(selectorType string, field javaObjectField) (string, error) {
	enumType := field.class.declaration.Symbol
	ordinal := slices.IndexFunc(field.class.enumConstants, func(constant Occurrence) bool {
		return constant.Symbol == field.declaration.Symbol
	})
	if ordinal == -1 {
		return "", fmt.Errorf("missing Java enum switch constant %s", field.declaration.Symbol)
	}
	// Match via the pre-constructor ordinal. A constructor may switch on
	// this before its singleton has been published to the static field.
	if selectorType == enumType {
		return fmt.Sprintf("javaSelector.Name() != nil && javaSelector.Ordinal() == int32(%d)", ordinal), nil
	}
	ctx.program.runtime = true
	return fmt.Sprintf("(func() bool {\njavaEnumView, javaEnumMatched := javart.MatchReference[%s](javaSelector)\nreturn javaEnumMatched && javaEnumView.Name() != nil && javaEnumView.Ordinal() == int32(%d)\n}())",
		ctx.program.objectGoType(enumType), ordinal), nil
}

func (ctx javaObjectContext) emitEnumInitialization(out *strings.Builder) error {
	class := ctx.class
	for ordinal, constant := range class.enumConstants {
		expression := constant.Initializer
		constructor := ctx.program.methods[expression.Symbol]
		if constructor == nil || (constructor.class != class && constructor.class.enumBase != class) ||
			constructor.declaration.Name != "<init>" {
			return fmt.Errorf("unsupported Java enum constructor for %s", constant.Name)
		}
		parameters := constructor.declaration.Parameters
		if len(expression.Arguments) != len(parameters) {
			return fmt.Errorf("unsupported Java enum constructor arity for %s", constant.Name)
		}
		out.WriteString("\t{\n")
		if constructor.class.enumBase != nil {
			fmt.Fprintf(out, "\t%s\n", ctx.program.classInitialization(constructor.class))
		}
		arguments := []string{enumNameArgument(constant.Name), fmt.Sprintf("int32(%d)", ordinal)}
		for index, argument := range expression.Arguments {
			name := fmt.Sprintf("javaEnumArgument%d", index)
			value, err := ctx.converted(argument, parameters[index].Type)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "\tvar %s %s = %s\n", name, ctx.program.objectGoType(parameters[index].Type), value)
			arguments = append(arguments, name)
		}
		field := javaObjectField{constant, class}
		value := fmt.Sprintf("%s(%s)", constructor.class.storageName(constructor.allocatorName()), strings.Join(arguments, ", "))
		if constructor.class.enumBase != nil {
			value += "." + objectViewName(class) + "()"
		}
		fmt.Fprintf(out, "\t%s = %s\n\t}\n", field.staticStorage(), value)
	}
	// Java publishes the values backing array only after all constants run
	// their constructors and before ordinary static initializers can use it.
	dimension := fmt.Sprintf("[]int32{%d}", len(class.enumConstants))
	values, err := ctx.arrayTypeCall(class.declaration.Symbol+"[]", "NewArray", dimension)
	if err != nil {
		return err
	}
	storage := class.enumValuesStorage()
	fmt.Fprintf(out, "\t%s = %s\n", storage, values)
	for index, constant := range class.enumConstants {
		field := javaObjectField{constant, class}
		fmt.Fprintf(out, "\t%s\n", ctx.arraySet(storage, fmt.Sprintf("int32(%d)", index), class.declaration.Symbol, field.staticStorage()))
	}
	return nil
}

func (ctx javaObjectContext) emitImplicitEnumMethod(out *strings.Builder, method *javaObjectMethod) {
	class := method.class
	storage := class.enumValuesStorage()
	if method.declaration.Name == "values" {
		fmt.Fprintf(out, "\treturn javart.EnumValues(%s)\n", storage)
		return
	}
	fmt.Fprintf(out, "\treturn javart.EnumValueOf[%s](%s, %q, parameter0)\n",
		ctx.program.objectGoType(class.declaration.Symbol), storage, class.declaration.Symbol)
}

func (ctx javaObjectContext) invokeJavaEnum(expression *JavaExpression) (string, error) {
	target := "this"
	if expression.Target != nil {
		value, err := ctx.expression(expression.Target)
		if err != nil {
			return "", err
		}
		target = value
	}
	ctx.program.runtime = true
	switch expression.Symbol {
	case javaEnumType + "#name()":
		return "javart.RequireNonNull(" + target + ").Name()", nil
	case javaEnumType + "#ordinal()":
		return "javart.RequireNonNull(" + target + ").Ordinal()", nil
	case javaEnumType + "#toString()":
		member := "ToString"
		if expression.Receiver == "super" {
			member = "Name"
		}
		return "javart.RequireNonNull(" + target + ")." + member + "()", nil
	case javaEnumType + "#hashCode()":
		return "javart.ObjectIdentityHash(" + target + ")", nil
	case javaEnumType + "#equals(java.lang.Object)":
		return ctx.emitReferenceInvocation(expression, Occurrence{Result: "boolean", Parameters: []Parameter{{Type: javaObjectType}}}, target,
			func(receiver, arguments string) string {
				return "javart.EnumEquals(" + receiver + ", " + arguments + ")"
			})
	case javaEnumType + "#compareTo(java.lang.Enum)":
		return ctx.emitReferenceInvocation(expression, Occurrence{Result: "int", Parameters: []Parameter{{Type: javaEnumType}}}, target,
			func(receiver, arguments string) string {
				return "javart.EnumCompare(" + receiver + ", " + arguments + ")"
			})
	default:
		return "", fmt.Errorf("unsupported Java Enum method %s", expression.Symbol)
	}
}
