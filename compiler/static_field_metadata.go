package compiler

import (
	"fmt"
	"strings"
)

func staticFieldMetadataRuntime() string {
	return `
// GppStaticField describes a declared static field and its typed access.
// Metadata is passive. Calling Get or Set can initialize Owner.
type GppStaticField struct {
	Name        string
	Owner       *GppClass
	Type        *GppType
	Final       bool
	Constant    bool
	Public      bool
	Annotations GppAnnotations
	Get         func() any
	Set         func(any) error
}

type StaticFieldRegistration struct {
	field GppStaticField
	goType gppReflect.Type
}

type __gppStaticFieldRegistration = StaticFieldRegistration

func TypedStaticField[T any](field GppStaticField) StaticFieldRegistration {
	return StaticFieldRegistration{field: field, goType: gppReflect.TypeFor[T]()}
}

func __gppTypedStaticField[T any](field GppStaticField) StaticFieldRegistration {
	return TypedStaticField[T](field)
}

// The registry keeps static metadata off the existing public GppClass and
// instance GppField layouts. The stored slices are immutable snapshots.
var gppStaticFieldRegistry gppSync.Map

func gppCloneStaticField(field GppStaticField) GppStaticField {
	if field.Type != nil {
		t := *field.Type
		field.Type = &t
	}
	field.Annotations = field.Annotations.Clone()
	return field
}

func ClassWithStaticFields(class *GppClass, fields []GppStaticField) *GppClass {
	declarations := make([]StaticFieldRegistration, len(fields))
	for index, field := range fields {
		declarations[index].field = field
	}
	return ClassWithTypedStaticFields(class, declarations)
}

func ClassWithTypedStaticFields(class *GppClass, fields []StaticFieldRegistration) *GppClass {
	declarations := make([]StaticFieldRegistration, len(fields))
	for index, field := range fields {
		declarations[index] = field
		declarations[index].field = gppCloneStaticField(field.field)
		declarations[index].field.Owner = class
	}
	gppStaticFieldRegistry.LoadOrStore(class, declarations)
	return class
}

func __gppClassWithTypedStaticFields(class *GppClass, fields []StaticFieldRegistration) *GppClass {
	return ClassWithTypedStaticFields(class, fields)
}

func SetStaticField[T any](name, typeName string, input any, set func(T)) error {
	if write, ok := input.(gppPrimitiveFieldWrite); ok {
		converted, valid := gppWidenFieldPrimitive(write.value, gppReflect.TypeFor[T]())
		if !valid {
			return GppInvocationError("static field " + name + " cannot be written with " + gppReflect.TypeOf(write.value).String())
		}
		input = converted
	}
	if input == nil {
		switch gppReflect.TypeFor[T]().Kind() {
		case gppReflect.Chan, gppReflect.Func, gppReflect.Interface, gppReflect.Map,
			gppReflect.Pointer, gppReflect.Slice, gppReflect.UnsafePointer:
			var zero T
			set(zero)
			return nil
		}
	}
	value, ok := input.(T)
	if !ok {
		return GppInvocationError("static field " + name + " requires " + typeName)
	}
	set(value)
	return nil
}

func __gppSetStaticField[T any](name, typeName string, input any, set func(T)) error {
	return SetStaticField(name, typeName, input, set)
}

func (class *GppClass) gppDeclaredStaticFields() []StaticFieldRegistration {
	if class == nil {
		return nil
	}
	stored, ok := gppStaticFieldRegistry.Load(class)
	if !ok {
		return nil
	}
	return stored.([]StaticFieldRegistration)
}

// DeclaredStaticFields returns every field declared by class, in source order.
func (class *GppClass) DeclaredStaticFields() []GppStaticField {
	var fields []GppStaticField
	for _, registration := range class.gppDeclaredStaticFields() {
		fields = append(fields, gppCloneStaticField(registration.field))
	}
	return fields
}

func (class *GppClass) DeclaredStaticField(name string) *GppStaticField {
	for _, registration := range class.gppDeclaredStaticFields() {
		field := registration.field
		if field.Name == name {
			result := gppCloneStaticField(field)
			return &result
		}
	}
	return nil
}

func (class *GppClass) visitPublicStaticFields(visit func(GppStaticField) bool) {
	seen := map[*GppClass]bool{}
	var walk func(*GppClass) bool
	walk = func(current *GppClass) bool {
		if current == nil || seen[current] {
			return true
		}
		seen[current] = true
		for _, registration := range current.gppDeclaredStaticFields() {
			field := registration.field
			if field.Public && !visit(field) {
				return false
			}
		}
		for _, parent := range current.Parents {
			if !walk(parent) {
				return false
			}
		}
		return true
	}
	walk(class)
}

// PublicStaticFields includes inherited public declarations, including hidden
// fields declared by separate classes. It visits each class once.
func (class *GppClass) PublicStaticFields() []GppStaticField {
	var fields []GppStaticField
	class.visitPublicStaticFields(func(field GppStaticField) bool {
		fields = append(fields, gppCloneStaticField(field))
		return true
	})
	return fields
}

// PublicStaticField searches the class before its declared parents.
func (class *GppClass) PublicStaticField(name string) *GppStaticField {
	var result *GppStaticField
	class.visitPublicStaticFields(func(field GppStaticField) bool {
		if field.Name != name {
			return true
		}
		snapshot := gppCloneStaticField(field)
		result = &snapshot
		return false
	})
	return result
}
`
}

func programHasStaticFields(files []*File) bool {
	for _, file := range files {
		for _, declaration := range file.Decls {
			if class, ok := declaration.(*ClassDecl); ok && len(class.StaticFields) != 0 {
				return true
			}
		}
	}
	return false
}

func classDescriptorParents(class *ClassDecl, classes map[string]*ClassDecl, context constructorContext) string {
	var parents []string
	for _, parent := range classParentNames(class) {
		if reference := classDescriptorReference(parent, classes, context); reference != "" {
			parents = append(parents, reference)
		}
	}
	if len(parents) == 0 {
		return "nil"
	}
	return "[]*GppClass{" + strings.Join(parents, ", ") + "}"
}

func staticFieldMetadataAccessor(class *ClassDecl, field Field) string {
	return "__gppStaticMetadata_" + class.Name + "_" + field.Name
}

func emitStaticFieldMetadata(out *strings.Builder, class *ClassDecl, context constructorContext) {
	out.WriteString(", []__gppStaticFieldRegistration{\n")
	for _, field := range class.StaticFields {
		accessor := staticFieldMetadataAccessor(class, field)
		sourceType := fieldTypeSource(field)
		fmt.Fprintf(out, "\t__gppTypedStaticField[%s](GppStaticField{Name: %q, Type: &GppType{Name: %q}, Public: %t, Final: %t, Constant: %t, Annotations: %s,\n",
			transformPolymorphicType(sourceType, context), field.Name, sourceType, memberVisibility(field.Name) == memberPublic, field.IsFinal || field.IsConstant,
			field.IsConstant, annotationUsesLiteral(field.Annotations, context))
		// Binding interface method values keeps the getter's dependencies out
		// of Go package initialization. A class initializer can consult its
		// own metadata without creating a declaration initialization cycle.
		fmt.Fprintf(out, "\t\tGet: interface { get() any }(%s{}).get, Set: interface { set(any) error }(%s{}).set}),\n",
			accessor, accessor)
	}
	out.WriteString("})")
}

func emitStaticFieldAccessors(out *strings.Builder, class *ClassDecl, classes map[string]*ClassDecl) {
	hasInitializer := needsClassInitialization(class, classes, map[*ClassDecl]bool{})
	for index := range class.StaticFields {
		field := &class.StaticFields[index]
		ref := staticFieldReference{owner: class, field: field}
		accessor := staticFieldMetadataAccessor(class, *field)
		fmt.Fprintf(out, "type %s struct{}\n", accessor)
		fmt.Fprintf(out, "func (%s) get() any {\n", accessor)
		if field.IsConstant {
			if hasInitializer {
				fmt.Fprintf(out, "\t%s()\n", classInitializationCall(class, ""))
			}
			fmt.Fprintf(out, "\treturn %s\n}\n", ref.constantName())
		} else {
			fmt.Fprintf(out, "\treturn %s()\n}\n", ref.accessor("Get"))
		}
		fmt.Fprintf(out, "func (%s) set(input any) error {\n", accessor)
		if hasInitializer {
			// Reflective access initializes the declaring class before type
			// checking or rejecting a write to a final field.
			fmt.Fprintf(out, "\t%s()\n", classInitializationCall(class, ""))
		}
		if field.IsConstant || field.IsFinal {
			message := "static field " + class.Name + "." + field.Name + " is final"
			fmt.Fprintf(out, "\treturn __gppInvocationError(%q)\n}\n\n", message)
			continue
		}
		fmt.Fprintf(out, "\treturn __gppSetStaticField(%q, %q, input, %s)\n}\n\n",
			class.Name+"."+field.Name, fieldTypeSource(*field), ref.accessor("Set"))
	}
}

func emitGenericStaticClassDescriptor(out *strings.Builder, class *ClassDecl, classes map[string]*ClassDecl, context constructorContext) {
	if len(class.StaticFields) == 0 {
		return
	}
	fmt.Fprintf(out, "var Gpp%sClass = __gppClassWithTypedStaticFields(&GppClass{Name: %q, Package: %q, Parents: %s, Annotations: %s}",
		class.Name, class.Name, templatePackageScope(context.ModulePath, context.Package),
		classDescriptorParents(class, classes, context), annotationUsesLiteral(class.Annotations, context))
	emitStaticFieldMetadata(out, class, context)
	out.WriteString("\n\n")
	emitStaticFieldAccessors(out, class, classes)
	fmt.Fprintf(out, "func init() { __gppRegisterClass(Gpp%sClass) }\n\n", class.Name)
}
