package javaport

import (
	"fmt"
	"slices"
	"strings"
)

func (field javaObjectField) visibleOutsidePackage() bool {
	return slices.Contains(field.declaration.Modifiers, "PUBLIC") || slices.Contains(field.declaration.Modifiers, "PROTECTED")
}

func (field javaObjectField) final() bool {
	return slices.Contains(field.declaration.Modifiers, "FINAL")
}

func (field javaObjectField) bridgeName(operation string) string {
	return "GppJava" + operation + "Field_" + field.class.goTypeName() + "_" + field.declaration.Name
}

func (program *javaObjectProgram) emitInstanceFieldBridges(out *strings.Builder, class *javaObjectClass, className string) error {
	for _, declaration := range class.fields {
		field := javaObjectField{declaration: declaration, class: class}
		if slices.Contains(declaration.Modifiers, "STATIC") || !field.visibleOutsidePackage() {
			continue
		}
		get := field.bridgeName("Get")
		set := field.bridgeName("Set")
		if class.virtual[get] != nil || (!field.final() && class.virtual[set] != nil) {
			return unsupportedSource(class.file, declaration, "Go field accessor collides with a class method")
		}
		goType := program.objectGoType(declaration.Type)
		fmt.Fprintf(out, "\nfunc (this *%s) %s() %s { return this.javaField%s }\n", className, get, goType, declaration.Name)
		if !field.final() {
			fmt.Fprintf(out, "\nfunc (this *%s) %s(value %s) { this.javaField%s = value }\n", className, set, goType, declaration.Name)
		}
	}
	return nil
}

type javaInstanceField struct {
	field    javaObjectField
	receiver string
	bridge   bool
	explicit bool
}

func (ctx javaObjectContext) instanceField(expression *JavaExpression) (javaInstanceField, error) {
	field, found := ctx.program.fields[expression.Symbol]
	if !found || expression.Static {
		return javaInstanceField{}, fmt.Errorf("unsupported field access %s", expression.Symbol)
	}
	pkg := ctx.program.pkg
	if ctx.class != nil {
		pkg = ctx.class.file.Package
	} else if ctx.iface != nil {
		pkg = ctx.iface.file.Package
	}
	access := javaInstanceField{field: field, bridge: field.class.file.Package != pkg, explicit: expression.Target != nil}
	if access.bridge && !field.visibleOutsidePackage() {
		return javaInstanceField{}, fmt.Errorf("field %s is inaccessible outside its Java package", expression.Symbol)
	}
	if access.explicit {
		// javac's resolved declaration selects the correct owner view even
		// when a derived class hides a field with the same source name.
		receiver, err := ctx.converted(expression.Target, field.class.declaration.Symbol)
		if err != nil {
			return javaInstanceField{}, err
		}
		ctx.program.runtime = true
		access.receiver = receiver
		return access, nil
	}
	if ctx.class != nil && (expression.Receiver == "this" || expression.Receiver == "super") {
		if expression.Implicit {
			inherited := !slices.Contains(field.declaration.Modifiers, "PRIVATE")
			if receiver, ok := ctx.lexicalReceiver(field.class, inherited, false); ok {
				access.receiver = receiver
				return access, nil
			}
		}
		if receiver, ok := ctx.class.memberReceiver(field.class); ok {
			access.receiver = receiver
			return access, nil
		}
	}
	return javaInstanceField{}, fmt.Errorf("missing instance receiver for field %s", expression.Symbol)
}

func (access javaInstanceField) checkedReceiver(receiver string) string {
	if access.explicit {
		return "javart.RequireNonNull(" + receiver + ")"
	}
	return receiver
}

func (access javaInstanceField) read(receiver string) string {
	if access.bridge {
		return receiver + "." + access.field.bridgeName("Get") + "()"
	}
	return receiver + ".javaField" + access.field.declaration.Name
}

func (access javaInstanceField) write(receiver, value string) (string, error) {
	if access.bridge {
		if access.field.final() {
			return "", fmt.Errorf("cannot store to final field %s from another Java package", access.field.declaration.Symbol)
		}
		return receiver + "." + access.field.bridgeName("Set") + "(" + value + ")", nil
	}
	return receiver + ".javaField" + access.field.declaration.Name + " = " + value, nil
}

func (ctx javaObjectContext) assignInstanceField(target, source *JavaExpression) (string, error) {
	field, err := ctx.instanceField(target)
	if err != nil {
		return "", err
	}
	value, err := ctx.converted(source, target.Type)
	if err != nil {
		return "", err
	}
	stored := "javaFieldValue"
	if storageType := field.field.declaration.Type; storageType != target.Type {
		stored, err = ctx.convertValue(stored, target.Type, storageType)
		if err != nil {
			return "", err
		}
	}
	store, err := field.write(field.checkedReceiver("javaFieldReceiver"), stored)
	if err != nil {
		return "", err
	}
	goType := ctx.program.objectGoType(target.Type)
	// A plain field store evaluates its right operand before checking the
	// captured receiver for null. A compound update reads the field first.
	return fmt.Sprintf("func() %s {\n\tjavaFieldReceiver := %s\n\tvar javaFieldValue %s = %s\n\t%s\n\treturn javaFieldValue\n}()",
		goType, field.receiver, goType, value, store), nil
}
