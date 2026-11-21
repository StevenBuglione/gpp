package javaport

import (
	"fmt"
	"go/token"
	"maps"
	"slices"
	"strings"
)

type javaMemberDeclaration struct {
	file        SourceFile
	declaration Occurrence
}

func (program *javaObjectProgram) addMemberClasses(members map[string]javaMemberDeclaration) ([]*javaObjectClass, error) {
	program.memberTypeNames = map[string]string{}
	classes := make([]*javaObjectClass, 0, len(members))
	// Binary names put each enclosing class before its descendants. Reserve
	// ordinary top-level names before choosing member names.
	for _, binary := range slices.Sorted(maps.Keys(members)) {
		member := members[binary]
		declaration := eraseJavaObjectDeclaration(member.declaration)
		if !token.IsIdentifier(declaration.Name) || !token.IsExported(declaration.Name) ||
			!hasOnlyModifiers(declaration, "PUBLIC", "PROTECTED", "PRIVATE", "STATIC", "FINAL", "ABSTRACT", "SEALED", "NON_SEALED") {
			return nil, unsupportedSource(member.file, declaration, "unsupported Java member class declaration")
		}
		owner := program.classes[declaration.Owner]
		prefix := ""
		switch {
		case owner != nil:
			prefix = owner.goTypeName()
		case program.interfaces[declaration.Owner] != nil:
			prefix = program.interfaces[declaration.Owner].declaration.Name
		default:
			return nil, unsupportedSource(member.file, declaration, "unsupported enclosing Java member type")
		}
		base := prefix + "_" + declaration.Name
		exports := program.exports[member.file.Package]
		name := base
		for suffix := 2; exports[name]; suffix++ {
			name = fmt.Sprintf("%s_%d", base, suffix)
		}
		class := &javaObjectClass{file: member.file, declaration: declaration, goName: name, storagePackage: member.file.Package}
		if owner != nil && !slices.Contains(declaration.Modifiers, "STATIC") && declaration.Kind == "CLASS" {
			class.enclosing = owner
		}
		exports[name] = true
		program.classes[binary] = class
		program.memberTypeNames[strings.ReplaceAll(binary, "$", ".")] = binary
		classes = append(classes, class)
	}
	return classes, nil
}

func (class *javaObjectClass) goTypeName() string {
	if class.goName != "" {
		return class.goName
	}
	return class.declaration.Name
}

func (program *javaObjectProgram) memberTypeName(name string) string {
	if member := program.memberTypeNames[name]; member != "" {
		return member
	}
	component := strings.TrimRight(name, "[]")
	if member := program.memberTypeNames[component]; member != "" {
		return member + name[len(component):]
	}
	return name
}

func (program *javaObjectProgram) resolveMemberTypes(declaration Occurrence) Occurrence {
	if len(program.memberTypeNames) == 0 {
		return declaration
	}
	declaration.Type = program.memberTypeName(declaration.Type)
	declaration.Result = program.memberTypeName(declaration.Result)
	declaration.Parameters = slices.Clone(declaration.Parameters)
	for index := range declaration.Parameters {
		declaration.Parameters[index].Type = program.memberTypeName(declaration.Parameters[index].Type)
	}
	declaration.RecordComponents = slices.Clone(declaration.RecordComponents)
	for index := range declaration.RecordComponents {
		declaration.RecordComponents[index].Type = program.memberTypeName(declaration.RecordComponents[index].Type)
	}
	declaration.Supertypes = slices.Clone(declaration.Supertypes)
	for index := range declaration.Supertypes {
		declaration.Supertypes[index] = program.memberTypeName(declaration.Supertypes[index])
	}
	return declaration
}

func (ctx javaObjectContext) qualifiedThis(javaType string) (string, error) {
	receiver := "this"
	if !ctx.static {
		for current := ctx.class; current != nil; current = current.enclosing {
			if current.declaration.Symbol == javaType {
				return receiver, nil
			}
			receiver += ".javaEnclosing"
		}
	}
	return "", fmt.Errorf("missing Java enclosing instance %s.this", javaType)
}

func (ctx javaObjectContext) lexicalReceiver(owner *javaObjectClass, allowInherited, enclosingOnly bool) (string, bool) {
	receiver := "this"
	current := ctx.class
	if ctx.static || current == nil {
		return "", false
	}
	if enclosingOnly {
		current = current.enclosing
		receiver += ".javaEnclosing"
	}
	for ; current != nil; current = current.enclosing {
		if current == owner {
			return receiver, true
		}
		if allowInherited {
			if view, found := current.memberReceiver(owner); found {
				return receiver + strings.TrimPrefix(view, "this"), true
			}
		}
		receiver += ".javaEnclosing"
	}
	return "", false
}

func (ctx javaObjectContext) lexicalInterfaceReceiver(owner *javaInterface) string {
	receiver := "this"
	for current := ctx.class; current != nil; current = current.enclosing {
		if current.interfaces[owner.declaration.Symbol] != nil {
			return receiver
		}
		receiver += ".javaEnclosing"
	}
	return "this"
}

func (class *javaObjectClass) outermostName() string {
	return strings.SplitN(class.declaration.Symbol, "$", 2)[0]
}

func (program *javaObjectProgram) memberConstructorParameters(method *javaObjectMethod) (string, string) {
	parameters, arguments := program.objectParameterList(method.declaration.Parameters)
	if method.class.enclosing == nil {
		return parameters, arguments
	}
	outer := "javaOuter " + program.objectGoType(method.class.enclosing.declaration.Symbol)
	if parameters == "" {
		return outer, "javaOuter"
	}
	return outer + ", " + parameters, "javaOuter, " + arguments
}

func (ctx javaObjectContext) enclosingArgument(owner *javaObjectClass, target *JavaExpression, superclass bool) (string, error) {
	if target != nil {
		receiver, err := ctx.converted(target, owner.declaration.Symbol)
		if err != nil {
			return "", err
		}
		ctx.program.runtime = true
		return "javart.RequireNonNull(" + receiver + ")", nil
	}
	if receiver, ok := ctx.lexicalReceiver(owner, true, superclass); ok {
		return receiver, nil
	}
	return "", fmt.Errorf("missing required Java enclosing instance of %s", owner.declaration.Symbol)
}
