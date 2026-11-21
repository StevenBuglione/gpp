package javaport

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestNativeGPPStaticPrimitiveReflectionMatchesJava(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	reference := filepath.Join(root, "Probe.java")
	writeTestFile(t, reference, `import java.lang.reflect.*;

public class Probe {
    static String trace = "";
    public static class Numbers {
        public static boolean Boolean = true;
        public static byte Byte = -101;
        public static char Char = 65535;
        public static short Short = -21011;
        public static int Int = 16777217;
        public static long Long = 1152921573326323713L;
        public static float Float = -0.0f;
        public static double Double = 1.125;
        public static Object Reference = Integer.valueOf(17);
        public static final int Constant = 47;
    }
    public static class BadRead {
        static { trace += "R"; }
        public static Object Value = Integer.valueOf(17);
    }
    public static class BadWrite {
        static { trace += "W"; }
        public static boolean Value = true;
    }
    public static class FinalWrite {
        static { trace += "F"; }
        public static final int Value = 81;
    }

    static final String[] suffixes = {"Boolean", "Byte", "Char", "Short", "Int", "Long", "Float", "Double"};
    static final Class<?>[] types = {boolean.class, byte.class, char.class, short.class, int.class, long.class, float.class, double.class};
    static final Object[] values = {false, (byte) -7, (char) 43210, (short) -4567, -2147483648, -1152921573326323713L, 1.25f, -1.5};

    static String display(Object value) {
        if (value instanceof Character c) return Integer.toString(c);
        if (value instanceof Float f) return String.format("f%08x", Float.floatToRawIntBits(f));
        if (value instanceof Double d) return String.format("d%016x", Double.doubleToRawLongBits(d));
        return String.valueOf(value);
    }

    static String read(Field field, String suffix) throws Exception {
        try {
            return display(Field.class.getMethod("get" + suffix, Object.class).invoke(field, (Object) null));
        } catch (InvocationTargetException failure) {
            if (failure.getCause() instanceof IllegalArgumentException) return "error";
            throw failure;
        }
    }

    static boolean write(Field field, int kind) throws Exception {
        try {
            Field.class.getMethod("set" + suffixes[kind], Object.class, types[kind])
                    .invoke(field, null, values[kind]);
            return true;
        } catch (InvocationTargetException failure) {
            if (failure.getCause() instanceof IllegalArgumentException || failure.getCause() instanceof IllegalAccessException) return false;
            throw failure;
        }
    }

    public static void main(String[] args) throws Exception {
        for (String name : suffixes) {
            Field field = Numbers.class.getField(name);
            for (String suffix : suffixes) System.out.println(name + " => " + suffix + " " + read(field, suffix));
        }
        for (String name : suffixes) {
            Field field = Numbers.class.getField(name);
            for (int i = 0; i < types.length; i++) {
                System.out.println(name + " <= " + suffixes[i] + " " + write(field, i) + " " + display(field.get(null)));
            }
        }
        System.out.println("reference " + read(Numbers.class.getField("Reference"), "Int") + " " +
                write(Numbers.class.getField("Reference"), 4) + " " + Numbers.Reference);
        System.out.println("constant " + read(Numbers.class.getField("Constant"), "Long"));
        Field badRead = BadRead.class.getField("Value");
        Field badWrite = BadWrite.class.getField("Value");
        Field finalWrite = FinalWrite.class.getField("Value");
        System.out.println("lookup " + trace);
        System.out.println("read " + read(badRead, "Int") + " " + trace);
        System.out.println("write " + write(badWrite, 4) + " " + trace);
        System.out.println("final " + write(finalWrite, 1) + " " + trace);
    }
}
`)
	classes := filepath.Join(root, "classes")
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"), "--release", strconv.Itoa(release), "-d", classes, reference)
	want := runObjectTestCommand(t, root, java, "-cp", classes, "Probe")

	source := filepath.Join(root, "fields.gpp")
	writeTestFile(t, source, `package reference.fields

import "math"

var Trace string

type Alias = int32
type Defined int32

class Numbers {
    static Boolean bool = true
    static Byte int8 = -101
    static Char uint16 = 65535
    static Short int16 = -21011
    static Int Alias = 16777217
    static Long int64 = 1152921573326323713
    static Float float32 = math.Float32frombits(2147483648)
    static Double float64 = 1.125
    static Reference any = int32(17)
    static const Constant int32 = 47
    static Named Defined = 3
    static MachineInt int = 9
}
class BadRead {
    static { Trace += "R" }
    static Value any = int32(17)
}
class BadWrite {
    static { Trace += "W" }
    static Value bool = true
}
class FinalWrite {
    static { Trace += "F" }
    static const Value int32 = 81
}

func ReadConstant() (int64, error) {
    field := Numbers.class.publicStaticField("Constant")
    return field.getLong()
}
`)
	output := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions([]string{source}, output, compiler.CompileOptions{
		ModulePath: "example.com/field-reflection",
	}); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(output, "main.go"), `package main

import (
    "fmt"
    "math"

    fields "example.com/field-reflection/reference/fields"
)

type operation struct {
    name string
    get func(*fields.GppStaticField) (any, error)
    set func(*fields.GppStaticField) error
}

var operations = []operation{
    {"Boolean", func(f *fields.GppStaticField) (any, error) { return f.GetBoolean() }, func(f *fields.GppStaticField) error { return f.SetBoolean(false) }},
    {"Byte", func(f *fields.GppStaticField) (any, error) { return f.GetByte() }, func(f *fields.GppStaticField) error { return f.SetByte(-7) }},
    {"Char", func(f *fields.GppStaticField) (any, error) { return f.GetChar() }, func(f *fields.GppStaticField) error { return f.SetChar(43210) }},
    {"Short", func(f *fields.GppStaticField) (any, error) { return f.GetShort() }, func(f *fields.GppStaticField) error { return f.SetShort(-4567) }},
    {"Int", func(f *fields.GppStaticField) (any, error) { return f.GetInt() }, func(f *fields.GppStaticField) error { return f.SetInt(-2147483648) }},
    {"Long", func(f *fields.GppStaticField) (any, error) { return f.GetLong() }, func(f *fields.GppStaticField) error { return f.SetLong(-1152921573326323713) }},
    {"Float", func(f *fields.GppStaticField) (any, error) { return f.GetFloat() }, func(f *fields.GppStaticField) error { return f.SetFloat(1.25) }},
    {"Double", func(f *fields.GppStaticField) (any, error) { return f.GetDouble() }, func(f *fields.GppStaticField) error { return f.SetDouble(-1.5) }},
}

func display(value any) string {
    switch val := value.(type) {
    case float32:
        return fmt.Sprintf("f%08x", math.Float32bits(val))
    case float64:
        return fmt.Sprintf("d%016x", math.Float64bits(val))
    default:
        return fmt.Sprint(value)
    }
}

func result(value any, err error) string {
    if err != nil {
        return "error"
    }
    return display(value)
}

func main() {
    class := fields.GppNumbersClass
    for _, source := range operations {
        field := class.PublicStaticField(source.name)
        for _, target := range operations {
            fmt.Println(source.name, "=>", target.name, result(target.get(field)))
        }
    }
    for _, target := range operations {
        field := class.PublicStaticField(target.name)
        for _, source := range operations {
            fmt.Println(target.name, "<=", source.name, source.set(field) == nil, display(field.Get()))
        }
    }
    reference := class.PublicStaticField("Reference")
    fmt.Println("reference", result(reference.GetInt()), reference.SetInt(-2147483648) == nil, reference.Get())
    fmt.Println("constant", result(fields.ReadConstant()))
    badRead := fields.GppBadReadClass.PublicStaticField("Value")
    badWrite := fields.GppBadWriteClass.PublicStaticField("Value")
    finalWrite := fields.GppFinalWriteClass.PublicStaticField("Value")
    fmt.Println("lookup", fields.Trace)
    fmt.Println("read", result(badRead.GetInt()), fields.Trace)
    fmt.Println("write", badWrite.SetInt(-2147483648) == nil, fields.Trace)
    fmt.Println("final", finalWrite.SetByte(-7) == nil, fields.Trace)

    named := class.PublicStaticField("Named")
    machineInt := class.PublicStaticField("MachineInt")
    if _, err := named.GetInt(); err == nil || named.SetInt(13) == nil || named.Set(fields.Defined(13)) != nil || named.Get() != fields.Defined(13) {
        panic("defined Go scalar lost type identity")
    }
    if _, err := machineInt.GetInt(); err == nil || machineInt.SetInt(13) == nil || machineInt.Set(13) != nil || machineInt.Get() != 13 {
        panic("Go int lost native access")
    }
    wide := class.PublicStaticField("Long")
    if wide.Set(int32(5)) == nil || wide.SetInt(5) != nil || wide.Get() != int64(5) {
        panic("Go typed assignment and Java widening diverged")
    }
}
`)
	got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", ".")
	if got != want {
		t.Fatalf("GPP and Java primitive field reflection diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}
