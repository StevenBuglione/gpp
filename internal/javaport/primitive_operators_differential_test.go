package javaport

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestJavaPrimitiveOperatorsMatchOpenJDK(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	utility := filepath.Join(root, "PrimitiveOps.java")
	writeTestFile(t, utility, `package reference.operators;
public final class PrimitiveOps {
    private PrimitiveOps() {}
    public static int add(int left, int right) { return left + right; }
    public static int difference(char left, byte right) { return left - right; }
    public static int multiply(int left, int right) { return left * right; }
    public static int divide(int left, int right) { return left / right; }
    public static long longDivide(long left, long right) { return left / right; }
    public static long longRemainder(long left, long right) { return left % right; }
    public static int bitwise(char left, byte right) { return (left & right) ^ (left | right); }
    public static boolean relations(float left, int right) { return left < right || left > right; }
    public static boolean inclusive(int left, double right) { return left <= right && left >= right; }
    public static boolean boxedOrder(Integer left, Long right) { return left < right; }
    public static float floatRemainder(float left, float right) { return left % right; }
    public static double doubleDivision(double left, double right) { return left / right; }
    public static double doubleRemainder(double left, double right) { return left % right; }
    public static float strictFloat(float left, float right) { return left * right - 1.0f; }
    public static double strictDouble(double left, double right) { return left * right - 1.0; }
}
`)
	object := filepath.Join(root, "OperandOrder.java")
	writeTestFile(t, object, `package reference.operators;
public final class OperandOrder {
    private int changing = 4;
    private int calls;
    public OperandOrder() {}
    private int change() { changing = 7; return 2; }
    private boolean next() { calls = calls + 1; return true; }
    public int binaryOrder() { return changing - change(); }
    public int shiftOrder() { return changing << change(); }
    public int calls() { return calls; }
    public boolean eagerAnd() { return false & next(); }
    public boolean eagerOr() { return true | next(); }
    public boolean eagerXor() { return true ^ next(); }
    public boolean lazyAnd() { return false && next(); }
    public boolean lazyOr() { return true || next(); }
    public boolean boxedLeft(Boolean left) {
        try { return left && next(); }
        catch (NullPointerException failure) { return false; }
    }
    public String divideByZero(int right) {
        try { int ignored = change() / right; return "unexpected"; }
        catch (ArithmeticException failure) { return failure.getMessage(); }
    }
    public int remainderByZero(long right) {
        try { long ignored = change() % right; return 0; }
        catch (ArithmeticException failure) { return changing; }
    }
}
`)
	probe := filepath.Join(root, "Probe.java")
	writeTestFile(t, probe, `import reference.operators.PrimitiveOps;
import reference.operators.OperandOrder;

class Probe {
    public static void main(String[] args) {
        System.out.printf("%d %d %d %d %d %d %d%n",
            PrimitiveOps.add(Integer.MAX_VALUE, 1), PrimitiveOps.difference((char) 65535, (byte) -3),
            PrimitiveOps.multiply(65536, 65536), PrimitiveOps.divide(Integer.MIN_VALUE, -1),
            PrimitiveOps.longDivide(Long.MIN_VALUE, -1), PrimitiveOps.longRemainder(-11, 3),
            PrimitiveOps.bitwise((char) 32768, (byte) -3));
        System.out.printf("%b %b %b %b %b%n", PrimitiveOps.relations(Float.NaN, 3),
            PrimitiveOps.relations(2.5f, 3), PrimitiveOps.inclusive(3, 3.0),
            PrimitiveOps.boxedOrder(3, 5L), PrimitiveOps.boxedOrder(5, 3L));
        System.out.printf("%08x %08x %016x %016x %016x %08x %016x%n",
            Float.floatToIntBits(PrimitiveOps.floatRemainder(-6.0f, 3.0f)),
            Float.floatToIntBits(PrimitiveOps.floatRemainder(5.5f, 3.0f)),
            Double.doubleToLongBits(PrimitiveOps.doubleDivision(1.0, -0.0)),
            Double.doubleToLongBits(PrimitiveOps.doubleDivision(0.0, 0.0)),
            Double.doubleToLongBits(PrimitiveOps.doubleRemainder(-17.5, 3.0)),
            Float.floatToIntBits(PrimitiveOps.strictFloat(1.00000011920928955078125f, 0.99999988079071044921875f)),
            Double.doubleToLongBits(PrimitiveOps.strictDouble(1.000000000000000222044604925031308, 0.999999999999999777955395074968692)));
        System.out.printf("%d %d%n", new OperandOrder().binaryOrder(), new OperandOrder().shiftOrder());
        var logic = new OperandOrder();
        System.out.printf("%b %b %b %b %b %b %d%n", logic.eagerAnd(), logic.eagerOr(), logic.eagerXor(),
            logic.lazyAnd(), logic.lazyOr(), logic.boxedLeft(null), logic.calls());
        var failures = new OperandOrder();
        System.out.printf("%s %d%n", failures.divideByZero(0), failures.remainderByZero(0));
    }
}
`)
	classFiles := filepath.Join(root, "classes")
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"),
		"--release", strconv.Itoa(release), "-d", classFiles, utility, object, probe)
	want := runObjectTestCommand(t, root, java, "-cp", classFiles, "Probe")

	// The object and utility emitters have different entry points.
	utilityReport, err := Analyze(t.Context(), []string{utility}, Options{Java: java, Release: release, MethodBodies: true})
	if err != nil {
		t.Fatal(err)
	}
	utilityGPP, err := EmitGPP(utilityReport)
	if err != nil {
		t.Fatal(err)
	}
	objectReport, err := Analyze(t.Context(), []string{object}, Options{Java: java, Release: release, MethodBodies: true})
	if err != nil {
		t.Fatal(err)
	}
	objectGPP, err := EmitGPP(objectReport)
	if err != nil {
		t.Fatal(err)
	}
	utilityInput := filepath.Join(root, "utility.gpp")
	objectInput := filepath.Join(root, "object.gpp")
	writeTestFile(t, utilityInput, utilityGPP)
	writeTestFile(t, objectInput, objectGPP)
	output := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions([]string{utilityInput, objectInput}, output, compiler.CompileOptions{
		ModulePath: "example.com/java-operators",
	}); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(output, "main.go"), `package main

import (
    "fmt"
    "math"

    "example.com/java-operators/gpp/java"
    op "example.com/java-operators/reference/operators"
)

func javaDoubleBits(value float64) uint64 {
    if math.IsNaN(value) { return 0x7ff8000000000000 }
    return math.Float64bits(value)
}

func main() {
    fmt.Printf("%d %d %d %d %d %d %d\n",
        op.PrimitiveOpsAdd(math.MaxInt32, 1), op.PrimitiveOpsDifference(65535, -3),
        op.PrimitiveOpsMultiply(65536, 65536), op.PrimitiveOpsDivide(math.MinInt32, -1),
        op.PrimitiveOpsLongDivide(math.MinInt64, -1), op.PrimitiveOpsLongRemainder(-11, 3),
        op.PrimitiveOpsBitwise(32768, -3))
    fmt.Printf("%t %t %t %t %t\n", op.PrimitiveOpsRelations(float32(math.NaN()), 3),
        op.PrimitiveOpsRelations(2.5, 3), op.PrimitiveOpsInclusive(3, 3.0),
        op.PrimitiveOpsBoxedOrder(java.BoxInteger(3), java.BoxLong(5)), op.PrimitiveOpsBoxedOrder(java.BoxInteger(5), java.BoxLong(3)))
    fmt.Printf("%08x %08x %016x %016x %016x %08x %016x\n",
        math.Float32bits(op.PrimitiveOpsFloatRemainder(-6.0, 3.0)),
        math.Float32bits(op.PrimitiveOpsFloatRemainder(5.5, 3.0)),
        javaDoubleBits(op.PrimitiveOpsDoubleDivision(1.0, math.Copysign(0, -1))),
        javaDoubleBits(op.PrimitiveOpsDoubleDivision(0.0, 0.0)),
        javaDoubleBits(op.PrimitiveOpsDoubleRemainder(-17.5, 3.0)),
        math.Float32bits(op.PrimitiveOpsStrictFloat(1.00000011920928955078125, 0.99999988079071044921875)),
        javaDoubleBits(op.PrimitiveOpsStrictDouble(1.000000000000000222044604925031308, 0.999999999999999777955395074968692)))
    fmt.Printf("%d %d\n", op.NewOperandOrder().BinaryOrder(), op.NewOperandOrder().ShiftOrder())
    logic := op.NewOperandOrder()
    fmt.Printf("%t %t %t %t %t %t %d\n", logic.EagerAnd(), logic.EagerOr(), logic.EagerXor(),
        logic.LazyAnd(), logic.LazyOr(), logic.BoxedLeft(nil), logic.Calls())
    failures := op.NewOperandOrder()
    message, _ := failures.DivideByZero(0).ToGo()
    fmt.Printf("%s %d\n", message, failures.RemainderByZero(0))
}
`)
	got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", ".")
	if got != want {
		t.Fatalf("GPP and Java primitive operators diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}
