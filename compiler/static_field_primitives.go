package compiler

import (
	"fmt"
	"strings"
)

var javaFieldPrimitives = [...]struct {
	suffix string
	goType string
}{
	{"Boolean", "bool"},
	{"Byte", "int8"},
	{"Char", "uint16"},
	{"Short", "int16"},
	{"Int", "int32"},
	{"Long", "int64"},
	{"Float", "float32"},
	{"Double", "float64"},
}

func staticPrimitiveReflectionRuntime() string {
	var out strings.Builder
	out.WriteString(`
type gppPrimitiveFieldWrite struct{ value any }

func gppWidenFieldPrimitive(input any, target gppReflect.Type) (any, bool) {
	value := gppReflect.ValueOf(input)
	if !value.IsValid() || target == nil {
		return nil, false
	}
	// Defined Go types preserve their own identity. Go aliases of the
	// fixed-width Java representations have the same reflect.Type.
	if value.Type().PkgPath() != "" || target.PkgPath() != "" {
		return nil, false
	}
	if value.Type() == target {
		return input, true
	}
	from, to := value.Kind(), target.Kind()
	var allowed bool
	switch from {
	case gppReflect.Int8:
		allowed = to == gppReflect.Int16 || to == gppReflect.Int32 || to == gppReflect.Int64 || to == gppReflect.Float32 || to == gppReflect.Float64
	case gppReflect.Int16, gppReflect.Uint16:
		allowed = to == gppReflect.Int32 || to == gppReflect.Int64 || to == gppReflect.Float32 || to == gppReflect.Float64
	case gppReflect.Int32:
		allowed = to == gppReflect.Int64 || to == gppReflect.Float32 || to == gppReflect.Float64
	case gppReflect.Int64:
		allowed = to == gppReflect.Float32 || to == gppReflect.Float64
	case gppReflect.Float32:
		allowed = to == gppReflect.Float64
	}
	if !allowed {
		return nil, false
	}
	if from == gppReflect.Int64 && to == gppReflect.Float32 {
		// reflect.Value.Convert passes through float64, which can round twice.
		return float32(value.Int()), true
	}
	return value.Convert(target).Interface(), true
}

func (field GppStaticField) gppTypedRegistration() *StaticFieldRegistration {
	declarations := field.Owner.gppDeclaredStaticFields()
	for index := range declarations {
		if declarations[index].field.Name == field.Name && declarations[index].goType != nil {
			return &declarations[index]
		}
	}
	return nil
}

func gppGetStaticPrimitive[T any](field GppStaticField) (T, error) {
	var zero T
	target := gppReflect.TypeFor[T]()
	if registration := field.gppTypedRegistration(); registration != nil {
		// Read through the declaring class accessor before checking the conversion.
		value := registration.field.Get()
		// An interface field can hold a value that looks primitive at runtime.
		// Only primitive storage is eligible for Java primitive access.
		if registration.goType == gppReflect.TypeOf(value) {
			if widened, ok := gppWidenFieldPrimitive(value, target); ok {
				return widened.(T), nil
			}
		}
	}
	return zero, GppInvocationError("static field " + field.Name + " cannot be read as " + target.String())
}

func gppSetStaticPrimitive(field GppStaticField, value any) error {
	if registration := field.gppTypedRegistration(); registration != nil {
		return registration.field.Set(gppPrimitiveFieldWrite{value: value})
	}
	return GppInvocationError("static field " + field.Name + " has no typed accessor")
}
`)
	for _, primitive := range javaFieldPrimitives {
		fmt.Fprintf(&out, `
func (field GppStaticField) Get%s() (%s, error) {
	return gppGetStaticPrimitive[%s](field)
}

func (field GppStaticField) Set%s(value %s) error {
	return gppSetStaticPrimitive(field, value)
}
`, primitive.suffix, primitive.goType, primitive.goType, primitive.suffix, primitive.goType)
	}
	return out.String()
}

func staticPrimitiveReflectionSelector(name string) (string, bool) {
	if strings.HasPrefix(name, "get") || strings.HasPrefix(name, "set") {
		for _, primitive := range javaFieldPrimitives {
			if name[3:] == primitive.suffix {
				return strings.ToUpper(name[:1]) + name[1:], true
			}
		}
	}
	return "", false
}
