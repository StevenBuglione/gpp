package javaport

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestJavaBoxingMatchesOpenJDK(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	utility := filepath.Join(root, "Boxes.java")
	writeTestFile(t, utility, `package reference.boxes.utility;
public final class Boxes {
    private Boxes() {}

    public static Boolean boxBoolean(boolean value) { return value; }
    public static Byte boxByte(byte value) { return value; }
    public static Short boxShort(short value) { return value; }
    public static Character boxChar(char value) { return value; }
    public static Integer boxInt(int value) { return value; }
    public static Long boxLong(long value) { return value; }
    public static Float boxFloat(float value) { return value; }
    public static Double boxDouble(double value) { return value; }
    public static Byte narrowByte() { return 127; }
    public static Short narrowShort() { return (char) 1234; }
    public static Character narrowChar() { return 65535; }
    public static boolean singleton() { return Boolean.TRUE == (Boolean) true; }
    public static Integer valueOf(int value) { return Integer.valueOf(value); }
    public static Integer construct(int value) { return new Integer(value); }
    public static Float constructFloatFromDouble() { return new Float(Double.MAX_VALUE); }
    public static boolean invert(Boolean value) { return !value; }
    public static int shift(Byte value, Long distance) { return value << distance; }
    public static float floatValue(Float value) { return value; }
    public static double doubleValue(Double value) { return value; }
    public static long widen(Integer value) { return (long) value; }
    public static Object toObject(int value) { return value; }
    public static Number toNumber(int value) { return (Number) value; }
    public static Integer fromNumber(Number value) { return (Integer) value; }
    public static long numberLong(Number value) { return value.longValue(); }
    public static float numberFloat(Number value) { return value.floatValue(); }
    public static int fromObject(Object value) { return (int) value; }
    public static boolean literalNull() { return (Integer) null == null; }
    public static boolean nullNumber(Integer value) { return (Number) value == null; }
    public static boolean nullObject(Integer value) { return (Object) value == null; }
    public static boolean equalPrimitive(Integer value) { return value == 127; }
    public static boolean equalReference(Integer left, Integer right) { return left == right; }
}
`)
	object := filepath.Join(root, "BoxingCases.java")
	writeTestFile(t, object, `package reference.boxes.object;
public final class BoxingCases {
    private Integer field = 127;
    private int calls;
    private int observed = 127;

    public BoxingCases(Short value) { field = value.intValue(); }
    public BoxingCases(short value) { field = (int) value; }

    public int storedValue() { return field; }

    public long assignments(int value) {
        Integer local = 1;
        local = value;
        field = local + 2;
        return field;
    }
    private Integer next() {
        calls = calls + 1;
        return calls;
    }
    private int order(Integer left, long right) {
        return left * 10 + (int) right;
    }
    public int evaluationOrder() { return order(next(), next()); }
    private int replaceObserved() {
        observed = 128;
        return 127;
    }
    public boolean comparisonOrder() { return observed == replaceObserved(); }
    public int calls() { return calls; }
    public byte numericMethod(Long value) { return value.byteValue(); }
    public double widenedMethod(Character value) { return value.charValue(); }
    public boolean condition(Boolean value) {
        if (value) { return true; }
        return false;
    }
    public boolean sameObject(Integer value) {
        Number number = value;
        Object object = number;
        return object == number;
    }
    public int nullUnbox() {
        try {
            Integer empty = null;
            return empty;
        }
        catch (NullPointerException failure) { return 11; }
    }
    public int nullReceiver() {
        try {
            Integer empty = null;
            return empty.intValue();
        }
        catch (NullPointerException failure) { return 22; }
    }
    public int failedCast() {
        try {
            Object wrong = "not a wrapper";
            return (int) wrong;
        }
        catch (ClassCastException failure) { return 33; }
    }
    public int failedNumberCast() {
        try {
            Number wrong = Long.valueOf(7L);
            return (Integer) wrong;
        }
        catch (ClassCastException failure) { return 44; }
    }
    public int nullCastAndUnbox() {
        try {
            Object empty = null;
            return (int) empty;
        }
        catch (NullPointerException failure) { return 55; }
    }
    public int nullNumberMethod() {
        try {
            Number empty = null;
            return empty.byteValue();
        }
        catch (NullPointerException failure) { return 66; }
    }
}
`)
	contract := filepath.Join(root, "BoxContract.java")
	writeTestFile(t, contract, `package reference.boxes.contract;
public interface BoxContract {
    Character convert(Integer value);
}
`)
	reference := filepath.Join(root, "Probe.java")
	writeTestFile(t, reference, `import reference.boxes.utility.Boxes;
import reference.boxes.object.BoxingCases;

class Probe {
    public static void main(String[] args) {
        System.out.printf("%b %b %b %b %b %b %b %b%n",
            Boxes.boxBoolean(true) == Boxes.boxBoolean(true),
            Boxes.boxByte((byte) -128) == Boxes.boxByte((byte) -128),
            Boxes.boxShort((short) 127) == Boxes.boxShort((short) 127),
            Boxes.boxChar((char) 127) == Boxes.boxChar((char) 127),
            Boxes.boxInt(-128) == Boxes.boxInt(-128),
            Boxes.boxLong(127L) == Boxes.boxLong(127L),
            Boxes.boxFloat(5f) == Boxes.boxFloat(5f),
            Boxes.boxDouble(5d) == Boxes.boxDouble(5d));
        System.out.printf("%b %b %b %b %b %b %b%n",
            Boxes.boxInt(128) == Boxes.boxInt(128),
            Boxes.boxChar((char) 128) == Boxes.boxChar((char) 128),
            Boxes.boxShort((short) -129) == Boxes.boxShort((short) -129),
            Boxes.boxLong(128L) == Boxes.boxLong(128L),
            Boxes.valueOf(127) == Boxes.boxInt(127),
            Boxes.construct(127) == Boxes.boxInt(127), Boxes.singleton());
        System.out.printf("%d %d %d %b %d %08x %016x%n",
            Boxes.narrowByte(), Boxes.narrowShort(), (int) Boxes.narrowChar(), Boxes.invert(false),
            Boxes.shift((byte) -128, 33L),
            Float.floatToRawIntBits(Boxes.floatValue(Float.intBitsToFloat(0x7fc01234))),
            Double.doubleToRawLongBits(Boxes.doubleValue(-0.0d)));
        System.out.printf("%d %d %d %b %b %b %b %b%n",
            Boxes.widen(Integer.MAX_VALUE), Boxes.fromNumber(Boxes.toNumber(-7)),
            Boxes.fromObject(Boxes.toObject(12)), Boxes.nullNumber(null), Boxes.nullObject(null),
            Boxes.equalPrimitive(127), Boxes.equalReference(127, 127),
            Boxes.equalReference(128, 128));
        System.out.printf("%d %d %08x %08x %b%n", Boxes.numberLong(Long.MAX_VALUE),
            Boxes.numberLong(Double.POSITIVE_INFINITY),
            Float.floatToRawIntBits(Boxes.numberFloat(Float.intBitsToFloat(0x7fc01234))),
            Float.floatToRawIntBits(Boxes.constructFloatFromDouble()),
            Boxes.literalNull());
        var cases = new BoxingCases((short) -3);
        System.out.printf("%d %d %d %d %d %d %d %b %b %b %b%n",
            new BoxingCases((Short) (short) 9).storedValue(), cases.storedValue(),
            cases.assignments(125), cases.evaluationOrder(), cases.calls(), cases.numericMethod(511L),
            (int) cases.widenedMethod((char) 65535), cases.condition(true),
            cases.sameObject(127), cases.sameObject(null), cases.comparisonOrder());
        System.out.printf("%d %d %d %d %d %d%n", cases.nullUnbox(),
            cases.nullReceiver(), cases.failedCast(), cases.failedNumberCast(), cases.nullCastAndUnbox(),
            cases.nullNumberMethod());
    }
}
`)
	classes := filepath.Join(root, "classes")
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"),
		"--release", strconv.Itoa(release), "-d", classes, utility, object, contract, reference)
	want := runObjectTestCommand(t, root, java, "-cp", classes, "Probe")

	report, err := Analyze(t.Context(), []string{utility, object, contract}, Options{Java: java, Release: release, MethodBodies: true})
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
		ModulePath: "example.com/java-boxes",
	}); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(output, "main.go"), `package main

import (
    "fmt"
    "math"

    "example.com/java-boxes/reference/boxes/contract"
    "example.com/java-boxes/reference/boxes/object"
    "example.com/java-boxes/reference/boxes/utility"
    java "example.com/java-boxes/gpp/java"
)

type goContract struct{}

func (goContract) Convert(value *java.Integer) *java.Character {
    return java.BoxCharacter(uint16(java.UnboxInteger(value)))
}

var _ contract.BoxContract = goContract{}

func main() {
    fmt.Printf("%t %t %t %t %t %t %t %t\n",
        utility.BoxesBoxBoolean(true) == utility.BoxesBoxBoolean(true),
        utility.BoxesBoxByte(-128) == utility.BoxesBoxByte(-128),
        utility.BoxesBoxShort(127) == utility.BoxesBoxShort(127),
        utility.BoxesBoxChar(127) == utility.BoxesBoxChar(127),
        utility.BoxesBoxInt(-128) == utility.BoxesBoxInt(-128),
        utility.BoxesBoxLong(127) == utility.BoxesBoxLong(127),
        utility.BoxesBoxFloat(5) == utility.BoxesBoxFloat(5),
        utility.BoxesBoxDouble(5) == utility.BoxesBoxDouble(5))
    fmt.Printf("%t %t %t %t %t %t %t\n",
        utility.BoxesBoxInt(128) == utility.BoxesBoxInt(128),
        utility.BoxesBoxChar(128) == utility.BoxesBoxChar(128),
        utility.BoxesBoxShort(-129) == utility.BoxesBoxShort(-129),
        utility.BoxesBoxLong(128) == utility.BoxesBoxLong(128),
        utility.BoxesValueOf(127) == utility.BoxesBoxInt(127),
        utility.BoxesConstruct(127) == utility.BoxesBoxInt(127), utility.BoxesSingleton())
    fmt.Printf("%d %d %d %t %d %08x %016x\n",
        java.UnboxByte(utility.BoxesNarrowByte()), java.UnboxShort(utility.BoxesNarrowShort()),
        java.UnboxCharacter(utility.BoxesNarrowChar()), utility.BoxesInvert(java.BoxBoolean(false)),
        utility.BoxesShift(java.BoxByte(-128), java.BoxLong(33)),
        math.Float32bits(utility.BoxesFloatValue(java.BoxFloat(math.Float32frombits(0x7fc01234)))),
        math.Float64bits(utility.BoxesDoubleValue(java.BoxDouble(math.Copysign(0, -1)))))
    fmt.Printf("%d %d %d %t %t %t %t %t\n",
        utility.BoxesWiden(java.BoxInteger(math.MaxInt32)),
        java.UnboxInteger(utility.BoxesFromNumber(utility.BoxesToNumber(-7))),
        utility.BoxesFromObject(utility.BoxesToObject(12)), utility.BoxesNullNumber(nil), utility.BoxesNullObject(nil),
        utility.BoxesEqualPrimitive(java.BoxInteger(127)),
        utility.BoxesEqualReference(java.BoxInteger(127), java.BoxInteger(127)),
        utility.BoxesEqualReference(java.BoxInteger(128), java.BoxInteger(128)))
    fmt.Printf("%d %d %08x %08x %t\n", utility.BoxesNumberLong(java.BoxLong(math.MaxInt64)),
        utility.BoxesNumberLong(java.BoxDouble(math.Inf(1))),
        math.Float32bits(utility.BoxesNumberFloat(java.BoxFloat(math.Float32frombits(0x7fc01234)))),
        math.Float32bits(java.UnboxFloat(utility.BoxesConstructFloatFromDouble())),
        utility.BoxesLiteralNull())
    cases := object.NewBoxingCasesWithShort(-3)
    fmt.Printf("%d %d %d %d %d %d %d %t %t %t %t\n",
        object.NewBoxingCasesWithBoxedShort(java.BoxShort(9)).StoredValue(), cases.StoredValue(),
        cases.Assignments(125), cases.EvaluationOrder(), cases.Calls(), cases.NumericMethod(java.BoxLong(511)),
        int32(cases.WidenedMethod(java.BoxCharacter(65535))), cases.Condition(java.BoxBoolean(true)),
        cases.SameObject(java.BoxInteger(127)), cases.SameObject(nil), cases.ComparisonOrder())
    fmt.Printf("%d %d %d %d %d %d\n", cases.NullUnbox(), cases.NullReceiver(), cases.FailedCast(),
        cases.FailedNumberCast(), cases.NullCastAndUnbox(), cases.NullNumberMethod())
}
`)
	got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", ".")
	if got != want {
		t.Fatalf("GPP and Java boxing results diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}
