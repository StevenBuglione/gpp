package javaport

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestEmitPrimitiveUtilityClassAndCallFromGo(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	path := filepath.Join(root, "PrimitiveCasts.java")
	writeTestFile(t, path, `package reference.primitive;
public final class PrimitiveCasts {
    private PrimitiveCasts() {}
    public static long toLong(double value) { return (long) (value); }
    public static short toShort(double value) { return (short) value; }
    public static char toChar(float value) { return (char) value; }
    public static byte toByte(int value) { return (byte) value; }
    public static long widen(int value) { return value; }
    public static float toFloat(double value) { return (float) value; }
    public static int unsignedShort(short value) { return value >>> 16; }
    public static int unsigned(int value, long distance) { return value >>> distance; }
    public static long left(long value, long distance) { return value << distance; }
    public static long right(long value, int distance) { return value >> distance; }
}
`)
	report, err := Analyze(t.Context(), []string{path}, Options{
		Java: java, Release: release, MethodBodies: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	gpp, err := EmitGPP(report)
	if err != nil {
		t.Fatal(err)
	}
	gppFile := filepath.Join(root, "primitive_casts.gpp")
	writeTestFile(t, gppFile, gpp)
	output := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions([]string{gppFile}, output,
		compiler.CompileOptions{ModulePath: "example.com/java-primitive-methods"}); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(output, "main.go"), `package main

import (
	"fmt"
	"math"
	"example.com/java-primitive-methods/reference/primitive"
)

func main() {
	fmt.Println(primitive.PrimitiveCastsToLong(math.Inf(1)))
	fmt.Println(primitive.PrimitiveCastsToLong(math.NaN()))
	fmt.Println(primitive.PrimitiveCastsToShort(math.Inf(1)))
	fmt.Println(primitive.PrimitiveCastsToChar(float32(math.Inf(-1))))
	fmt.Println(primitive.PrimitiveCastsToByte(255))
	fmt.Println(primitive.PrimitiveCastsWiden(-2147483648))
	fmt.Println(math.IsInf(float64(primitive.PrimitiveCastsToFloat(math.MaxFloat64)), 1))
	fmt.Println(primitive.PrimitiveCastsUnsignedShort(-1))
	fmt.Println(primitive.PrimitiveCastsUnsigned(-1, -1))
	fmt.Println(primitive.PrimitiveCastsLeft(2, 65))
	fmt.Println(primitive.PrimitiveCastsRight(-4, 33))
}
`)

	command := exec.CommandContext(t.Context(), "go", "run", "-buildvcs=false", ".")
	command.Dir = output
	result, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Go consumer: %v\n%s", err, result)
	}
	if got, want := string(result), "9223372036854775807\n0\n-1\n0\n-1\n-2147483648\ntrue\n65535\n1\n4\n-1\n"; got != want {
		t.Fatalf("Go consumer output:\n%s\nwant:\n%s", got, want)
	}
}

func TestUtilityEmissionRejectsStaticInitializer(t *testing.T) {
	java, release := referenceJDK(t)
	path := filepath.Join(t.TempDir(), "Example.java")
	writeTestFile(t, path, `package reference.primitive;
public final class Example {
    static { System.nanoTime(); }
    private Example() {}
    public static long toLong(double value) { return (long) value; }
}
`)
	report, err := Analyze(t.Context(), []string{path}, Options{
		Java: java, Release: release, MethodBodies: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if output, err := EmitGPP(report); output != "" || err == nil ||
		!strings.Contains(err.Error(), "unsupported utility-class member") {
		t.Fatalf("initializer should block emission, got %q with %v", output, err)
	}
}
