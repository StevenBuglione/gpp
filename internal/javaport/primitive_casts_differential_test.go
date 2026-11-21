package javaport

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestJavaExplicitPrimitiveCastsMatchOpenJDK(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	utility := filepath.Join(root, "UtilityCasts.java")
	writeTestFile(t, utility, `package reference.casts.utility;
public final class UtilityCasts {
    private UtilityCasts() {}

    public static byte narrowLiteral() { return (byte) 255; }
    public static char narrowNegativeLiteral() { return (char) -1; }
    public static int negateMin() { return -Integer.MIN_VALUE; }
    public static long negateMinLong() { return -Long.MIN_VALUE; }
    public static int complement(char value) { return ~value; }
    public static int unaryPlus(byte value) { return +value; }
    public static boolean invert(boolean value) { return !value; }
    public static byte narrowShift() { return (byte) ((1 << 31) >> 31); }
    public static int unsignedShiftLiteral() { return (int) (-1L >>> -63); }
    public static int intNaN() { return (int) Double.NaN; }
    public static long longInfinity() { return (long) Double.POSITIVE_INFINITY; }
    public static short shortInfinity() { return (short) Float.POSITIVE_INFINITY; }
    public static byte byteNegativeInfinity() { return (byte) Double.NEGATIVE_INFINITY; }
    public static char charInfinity() { return (char) Float.POSITIVE_INFINITY; }
    public static float narrowDouble() { return (float) Double.MAX_VALUE; }
    public static float negativeUnderflow() { return (float) -Double.MIN_VALUE; }
    public static double negativeZero() { return (double) -0.0f; }
    public static double rawDoubleNaN() { return Double.NaN; }
    public static float rawFloatNaN() { return Float.NaN; }
    public static int intFromDouble(double value) { return (int) value; }
    public static long longFromDouble(double value) { return (long) value; }
}
`)
	object := filepath.Join(root, "ObjectCasts.java")
	writeTestFile(t, object, `package reference.casts.object;
public final class ObjectCasts {
    private int calls;

    public ObjectCasts() {}

    public double next() { calls = calls + 1; return 128.9; }
    public byte narrowOnce() { return (byte) -next(); }
    public int calls() { return calls; }
    public char narrowLocal(char initial) {
        char local = initial;
        return (char) -local;
    }
    public int shiftField() { return calls << 31; }
    private int advance() { calls = calls + 1; return calls; }
    public int shiftEvaluationOrder() { return advance() << advance(); }
    public int classConstant() { return (int) Double.NaN; }
}
`)
	reference := filepath.Join(root, "Probe.java")
	writeTestFile(t, reference, `import reference.casts.utility.UtilityCasts;
import reference.casts.object.ObjectCasts;

class Probe {
    public static void main(String[] args) {
        System.out.printf("%d %d %d %d %d %d %b %d %d%n",
            UtilityCasts.narrowLiteral(), (int) UtilityCasts.narrowNegativeLiteral(),
            UtilityCasts.negateMin(), UtilityCasts.negateMinLong(),
            UtilityCasts.complement((char) 65535), UtilityCasts.unaryPlus((byte) -128),
            UtilityCasts.invert(false), UtilityCasts.narrowShift(), UtilityCasts.unsignedShiftLiteral());
        System.out.printf("%d %d %d %d %d%n",
            UtilityCasts.intNaN(), UtilityCasts.longInfinity(), UtilityCasts.shortInfinity(),
            UtilityCasts.byteNegativeInfinity(), (int) UtilityCasts.charInfinity());
        System.out.printf("%08x %08x %016x %016x %08x%n",
            Float.floatToRawIntBits(UtilityCasts.narrowDouble()), Float.floatToRawIntBits(UtilityCasts.negativeUnderflow()),
            Double.doubleToRawLongBits(UtilityCasts.negativeZero()), Double.doubleToRawLongBits(UtilityCasts.rawDoubleNaN()),
            Float.floatToRawIntBits(UtilityCasts.rawFloatNaN()));
        for (double input : new double[] {
                Double.NaN, Double.NEGATIVE_INFINITY, Double.POSITIVE_INFINITY, -1.9, 1.9,
                Math.nextDown(0x1p31), 0x1p31, -0x1p31, Math.nextDown(-0x1p31),
                Math.nextDown(0x1p63), 0x1p63, -0x1p63, Math.nextDown(-0x1p63)}) {
            System.out.printf("%d %d%n", UtilityCasts.intFromDouble(input), UtilityCasts.longFromDouble(input));
        }
        var instance = new ObjectCasts();
        System.out.printf("%d %d %d %d %d %d %d%n",
            instance.narrowOnce(), instance.calls(), (int) instance.narrowLocal((char) 16),
            instance.shiftField(), instance.classConstant(), instance.shiftEvaluationOrder(), instance.calls());
    }
}
`)
	classes := filepath.Join(root, "classes")
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"),
		"--release", strconv.Itoa(release), "-d", classes, utility, object, reference)
	want := runObjectTestCommand(t, root, java, "-cp", classes, "Probe")

	report, err := Analyze(t.Context(), []string{utility, object}, Options{Java: java, Release: release, MethodBodies: true})
	if err != nil {
		t.Fatal(err)
	}
	units, err := EmitGPPPackages(report)
	if err != nil {
		t.Fatal(err)
	}
	inputs := make([]string, 0, len(units))
	for index, unit := range units {
		path := filepath.Join(root, strconv.Itoa(index)+".gpp")
		writeTestFile(t, path, unit.Source)
		inputs = append(inputs, path)
	}
	output := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions(inputs, output, compiler.CompileOptions{
		ModulePath: "example.com/java-casts",
	}); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(output, "main.go"), `package main

import (
    "fmt"
    "math"

    "example.com/java-casts/reference/casts/object"
    "example.com/java-casts/reference/casts/utility"
)

func main() {
    fmt.Printf("%d %d %d %d %d %d %t %d %d\n",
        utility.UtilityCastsNarrowLiteral(), utility.UtilityCastsNarrowNegativeLiteral(),
        utility.UtilityCastsNegateMin(), utility.UtilityCastsNegateMinLong(),
        utility.UtilityCastsComplement(65535), utility.UtilityCastsUnaryPlus(-128),
        utility.UtilityCastsInvert(false), utility.UtilityCastsNarrowShift(), utility.UtilityCastsUnsignedShiftLiteral())
    fmt.Printf("%d %d %d %d %d\n",
        utility.UtilityCastsIntNaN(), utility.UtilityCastsLongInfinity(), utility.UtilityCastsShortInfinity(),
        utility.UtilityCastsByteNegativeInfinity(), utility.UtilityCastsCharInfinity())
    fmt.Printf("%08x %08x %016x %016x %08x\n",
        math.Float32bits(utility.UtilityCastsNarrowDouble()), math.Float32bits(utility.UtilityCastsNegativeUnderflow()),
        math.Float64bits(utility.UtilityCastsNegativeZero()), math.Float64bits(utility.UtilityCastsRawDoubleNaN()),
        math.Float32bits(utility.UtilityCastsRawFloatNaN()))
    for _, input := range []float64{
        math.NaN(), math.Inf(-1), math.Inf(1), -1.9, 1.9,
        math.Nextafter(0x1p31, 0), 0x1p31, -0x1p31, math.Nextafter(-0x1p31, math.Inf(-1)),
        math.Nextafter(0x1p63, 0), 0x1p63, -0x1p63, math.Nextafter(-0x1p63, math.Inf(-1)),
    } {
        fmt.Printf("%d %d\n", utility.UtilityCastsIntFromDouble(input), utility.UtilityCastsLongFromDouble(input))
    }

    instance := object.NewObjectCasts()
    fmt.Printf("%d %d %d %d %d %d %d\n",
        instance.NarrowOnce(), instance.Calls(), instance.NarrowLocal(16),
        instance.ShiftField(), instance.ClassConstant(), instance.ShiftEvaluationOrder(), instance.Calls())
}
`)
	got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", ".")
	if got != want {
		t.Fatalf("GPP and Java cast results diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}

func TestJavaExplicitPrimitiveCastRejectsUnsupportedOperand(t *testing.T) {
	java, release := referenceJDK(t)
	path := filepath.Join(t.TempDir(), "Example.java")
	writeTestFile(t, path, `package reference.casts;
public final class Example {
    private Example() {}
    public static byte narrow(int value) { return (byte) ++value; }
}
`)
	report, err := Analyze(t.Context(), []string{path}, Options{Java: java, Release: release, MethodBodies: true})
	if err != nil {
		t.Fatal(err)
	}
	if output, err := EmitGPP(report); output != "" || err == nil ||
		!strings.Contains(err.Error(), "unsupported expression PREFIX_INCREMENT at ") {
		t.Fatalf("expected unsupported cast operand with source position, got %q with %v", output, err)
	}
}
