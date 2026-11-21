package javaport

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestJavaConditionalExpressionsMatchOpenJDK(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	utility := filepath.Join(root, "Choices.java")
	writeTestFile(t, utility, `package reference.choices;
public final class Choices {
    private Choices() {}
    public static short narrow(boolean choose, short value) { return choose ? value : 7; }
    public static float promote(boolean choose, int value) { return choose ? value : 0.5f; }
    public static Boolean boxed(boolean choose, Boolean first, Boolean second) { return choose ? first : second; }
    public static Object contextualBox(boolean choose) { return choose ? 7 : null; }
}
`)
	object := filepath.Join(root, "Conditional.java")
	writeTestFile(t, object, `package reference.choices;
public final class Conditional {
    private int trace;
    public Conditional() {}
    private boolean first() { trace = trace * 10 + 1; return true; }
    private boolean second() { trace = trace * 10 + 2; return false; }
    private int left() { trace = trace * 10 + 3; return 30; }
    private int right() { trace = trace * 10 + 4; return 40; }
    public int trace() { return trace; }
    public int select(Boolean condition) {
        try { return condition ? left() : right(); }
        catch (NullPointerException failure) { return -1; }
    }
    public String nullString() { return first() ? null : null; }
    public boolean comparedNulls() { return (first() ? null : null) == (second() ? null : null); }
    public int lazyUnbox(boolean condition, Integer value) { return condition ? 3 : value; }
}
`)
	probe := filepath.Join(root, "Probe.java")
	writeTestFile(t, probe, `import reference.choices.Choices;
import reference.choices.Conditional;

class Probe {
    public static void main(String[] args) {
        System.out.printf("%d %d %08x %08x %b %b %b %b%n",
            Choices.narrow(true, (short) -4), Choices.narrow(false, (short) -4),
            Float.floatToRawIntBits(Choices.promote(true, 16777217)), Float.floatToRawIntBits(Choices.promote(false, 16777217)),
            Choices.boxed(true, Boolean.FALSE, null) == Boolean.FALSE, Choices.boxed(false, Boolean.FALSE, null) == null,
            Choices.contextualBox(true) == Integer.valueOf(7), Choices.contextualBox(false) == null);
        var object = new Conditional();
        System.out.printf("%d %d %d %b %b %d %d%n", object.select(true), object.select(false), object.select(null),
            object.nullString() == null, object.comparedNulls(), object.lazyUnbox(true, null), object.trace());
    }
}
`)
	classes := filepath.Join(root, "classes")
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"),
		"--release", strconv.Itoa(release), "-d", classes, utility, object, probe)
	want := runObjectTestCommand(t, root, java, "-cp", classes, "Probe")

	var inputs []string
	for index, origin := range []string{utility, object} {
		report, err := Analyze(t.Context(), []string{origin}, Options{Java: java, Release: release, MethodBodies: true})
		if err != nil {
			t.Fatal(err)
		}
		unit, err := EmitGPP(report)
		if err != nil {
			t.Fatal(err)
		}
		input := filepath.Join(root, "choice"+strconv.Itoa(index)+".gpp")
		writeTestFile(t, input, unit)
		inputs = append(inputs, input)
	}
	output := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions(inputs, output, compiler.CompileOptions{
		ModulePath: "example.com/conditional",
	}); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(output, "main.go"), `package main

import (
    "fmt"
    "math"

    "example.com/conditional/gpp/java"
    c "example.com/conditional/reference/choices"
)

func main() {
    fmt.Printf("%d %d %08x %08x %t %t %t %t\n",
        c.ChoicesNarrow(true, -4), c.ChoicesNarrow(false, -4),
        math.Float32bits(c.ChoicesPromote(true, 16777217)), math.Float32bits(c.ChoicesPromote(false, 16777217)),
        c.ChoicesBoxed(true, java.BoxBoolean(false), nil) == java.BoxBoolean(false),
        c.ChoicesBoxed(false, java.BoxBoolean(false), nil) == nil,
        c.ChoicesContextualBox(true) == java.BoxInteger(7), c.ChoicesContextualBox(false) == nil)
    object := c.NewConditional()
    fmt.Printf("%d %d %d %t %t %d %d\n", object.Select(trueBox()), object.Select(java.BoxBoolean(false)), object.Select(nil),
        object.NullString() == nil, object.ComparedNulls(), object.LazyUnbox(true, nil), object.Trace())
}

func trueBox() *java.Boolean { return java.BoxBoolean(true) }
`)
	got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", ".")
	if got != want {
		t.Fatalf("GPP and Java conditional expressions diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}
