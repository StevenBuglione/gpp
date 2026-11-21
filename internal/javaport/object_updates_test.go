package javaport

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestJavaCompoundAndIncrementExpressionsMatchOpenJDK(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	source := filepath.Join(root, "Updates.java")
	writeTestFile(t, source, `package reference.updates;
public final class Updates {
    private int field;
    private int trace;
    public Updates() {}
    public short narrow(short value) { return value += 4.6; }
    public int saveField() { field = 9; return field += (field = 3); }
    public int integralOperators(int left, int right) {
        left *= right;
        left -= 1;
        left /= right;
        left %= 127;
        left &= 255;
        left ^= 18;
        left |= 32;
        left <<= 37;
        left >>= -27;
        return left >>> 34;
    }
    public Boolean boxedBoolean(Boolean value, boolean other) { return value |= other; }
    public long boxedShift(Long value, Integer distance) { return value >>= distance; }
    private int right() { trace = trace * 10 + 3; return 5; }
    private int[] array(int[] data) { trace = trace * 10 + 1; return data; }
    private int index(int index) { trace = trace * 10 + 2; return index; }
    public int trace() { return trace; }
    public int updateArray(int[] data) { return array(data)[index(0)] += right(); }
    public int failedArray(int[] data, int at) {
        trace = 0;
        try { array(data)[index(at)] += right(); }
        catch (NullPointerException | ArrayIndexOutOfBoundsException failure) { return trace; }
        return -1;
    }
    public int nullBoxed(Integer[] data) {
        trace = 0;
        try { data[0] += right(); }
        catch (NullPointerException failure) { return trace; }
        return -1;
    }
    public int[] relocate(int[] data) {
        int[] previous = data;
        data[0] += ((data = new int[] {5})[0]);
        return previous;
    }
    public int narrowArray(byte[] data) {
        byte old = data[0]++;
        return old * 1000 + --data[1];
    }
    public boolean boxedPostfix(Integer[] data) {
        Integer before = data[0]++;
        return before == Integer.valueOf(127) && data[0] == 128;
    }
    public String stringUpdate(String[] data, char unit, float value) {
        data[0] += unit;
        data[0] += value;
        data[0] += Integer.valueOf(3);
        return data[0] += null;
    }
}
`)
	probe := filepath.Join(root, "Probe.java")
	writeTestFile(t, probe, `import reference.updates.Updates;

class Probe {
    public static void main(String[] args) {
        var updates = new Updates();
        System.out.printf("%d %d %d %b %d%n", updates.narrow((short) 3), updates.saveField(),
            updates.integralOperators(74, 9), updates.boxedBoolean(false, true) == Boolean.TRUE,
            updates.boxedShift(-1024L, 65));
        var numbers = new int[] {6};
        System.out.printf("%d %d %d %d %d %d%n", updates.updateArray(numbers), updates.trace(), numbers[0],
            updates.failedArray(null, 0), updates.failedArray(numbers, 2), updates.nullBoxed(new Integer[1]));
        var origin = new int[] {9};
        var narrow = new byte[] {127, -128};
        var boxed = new Integer[] {127};
        System.out.printf("%b %d %d %d %b %d%n", updates.relocate(origin) == origin, origin[0],
            updates.narrowArray(narrow), narrow[0], updates.boxedPostfix(boxed), boxed[0]);
        var strings = new String[1];
        var text = updates.stringUpdate(strings, '\ud800', 0.1f);
        System.out.println((text == strings[0]) + " " + text.length() + " " + (int) text.charAt(4) + " " + text.substring(5));
    }
}
`)
	classes := filepath.Join(root, "classes")
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"),
		"--release", strconv.Itoa(release), "-d", classes, source, probe)
	want := runObjectTestCommand(t, root, java, "-cp", classes, "Probe")

	report, err := Analyze(t.Context(), []string{source}, Options{Java: java, Release: release, MethodBodies: true})
	if err != nil {
		t.Fatal(err)
	}
	gpp, err := EmitGPP(report)
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(root, "updates.gpp")
	writeTestFile(t, input, gpp)
	output := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions([]string{input}, output, compiler.CompileOptions{
		ModulePath: "example.com/java-updates",
	}); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(output, "main.go"), `package main

import (
    "fmt"
    "unicode/utf16"

    "example.com/java-updates/gpp/java"
    "example.com/java-updates/reference/updates"
)

func main() {
    subject := updates.NewUpdates()
    fmt.Printf("%d %d %d %t %d\n", subject.Narrow(3), subject.SaveField(), subject.IntegralOperators(74, 9),
        subject.BoxedBoolean(java.BoxBoolean(false), true) == java.BoxBoolean(true),
        subject.BoxedShift(java.BoxLong(-1024), java.BoxInteger(65)))
    numbers := java.NewArray[int32](1, []int32{1})
    java.ArraySet(numbers, 0, int32(6))
    changed := subject.UpdateArray(numbers)
    fmt.Printf("%d %d %d %d %d %d\n", changed, subject.Trace(), java.ArrayGet[int32](numbers, 0),
        subject.FailedArray(nil, 0), subject.FailedArray(numbers, 2),
        subject.NullBoxed(java.NewArray[*java.Integer](1, []int32{1})))
    origin := java.NewArray[int32](1, []int32{1})
    java.ArraySet(origin, 0, int32(9))
    narrow := java.NewArray[int8](1, []int32{2})
    java.ArraySet(narrow, 0, int8(127))
    java.ArraySet(narrow, 1, int8(-128))
    boxed := java.NewArray[*java.Integer](1, []int32{1})
    java.ArraySet(boxed, 0, java.BoxInteger(127))
    same := subject.Relocate(origin) == origin
    wrapped := subject.NarrowArray(narrow)
    oldBox := subject.BoxedPostfix(boxed)
    fmt.Printf("%t %d %d %d %t %d\n", same, java.ArrayGet[int32](origin, 0), wrapped, java.ArrayGet[int8](narrow, 0),
        oldBox, java.UnboxInteger(java.ArrayGet[*java.Integer](boxed, 0)))
    strings := java.NewArray[*java.String](1, []int32{1})
    text := subject.StringUpdate(strings, 0xd800, 0.1)
    units := text.UTF16()
    fmt.Println(text == java.ArrayGet[*java.String](strings, 0), len(units), units[4], string(utf16.Decode(units[5:])))
}
`)
	got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", ".")
	if got != want {
		t.Fatalf("GPP and Java updates diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}
