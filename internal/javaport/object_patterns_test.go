package javaport

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestJavaReferencePatternsMatchOpenJDK(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	sources := []struct {
		name string
		body string
	}{
		{"Value.java", `package reference.patterns;
public interface Value {
    int score();
    default int inspect(Object value) {
        if (value instanceof String text) return text.length();
        return 0;
    }
}`},
		{"Base.java", `package reference.patterns;
public class Base { public int score() { return 1; } }`},
		{"Child.java", `package reference.patterns;
public class Child extends Base implements Value { public int score() { return 7; } }`},
		{"Lookalike.java", `package reference.patterns;
public class Lookalike { public int score() { return 5; } }`},
		{"Patterns.java", `package reference.patterns;
public class Patterns {
    private Object original = "prepare";
    private int prepared = original instanceof String text ? text.length() : -1;
    private int calls;
    private int received;

    public Patterns(Object value) { this(value instanceof String text ? text.length() : -1); }
    private Patterns(int count) { received = count; }
    public int setup() { return prepared * 10 + received; }

    private Object tap(Object value) {
        calls++;
        return value;
    }

    public int both(Object value) {
        calls = 0;
        if (tap(value) instanceof String text && text.length() > 1) return calls * 10 + text.length();
        return calls;
    }

    public int guarded(Object value) {
        calls = 0;
        if (!(tap(value) instanceof String text) || text.length() == 0) return -calls;
        return calls * 10 + text.length();
    }

    public int elseBinding(Object value) {
        if (!(value instanceof String text)) return 0;
        else return text.length();
    }

    public int completingThen(Object value) {
        if (value instanceof String text) {
            text = "new value";
        } else return 0;
        return text.length();
    }

    public int disjoint(Object value) {
        if (value instanceof String name) { return name.length(); }
        if (value instanceof int[] name) { return name.length; }
        return 0;
    }

    public int ternary(Object value) {
        return !(value instanceof String text) ? 0 : text.length();
    }

    public int iterations(Object[] values) {
        int index = 0;
        int result = 0;
        while (index < values.length && values[index++] instanceof String text) {
            if (text.length() == 1) continue;
            result += text.length();
        }
        for (int next = 0; next < values.length && values[next] instanceof String text; next++) {
            result = result * 10 + text.length();
        }
        return result;
    }

    public int whileExit(Object value) {
        while (!(value instanceof String text)) { value = "exit"; }
        return text.length();
    }

    public int doExit(Object value) {
        do { if (value == null) value = "done"; }
        while (!(value instanceof String text));
        return text.length();
    }

    public int base(Object value) {
        return value instanceof Base object ? object.score() : -1;
    }

    public int nominal(Object value) {
        return value instanceof Value object ? object.score() * 10 + object.inspect("sample") : -1;
    }

    public Object derivedArray() { return new Child[]{new Child()}; }

    public int array(Object value) {
        if (value instanceof Base[] items && items.length == 1) return items[0].score();
        return -1;
    }

    public int boxed(Object value) {
        if (value instanceof Integer number) { number += 2; return number; }
        return -1;
    }
}`},
	}
	files := make([]string, 0, len(sources))
	packageDir := filepath.Join(root, "reference", "patterns")
	if err := os.MkdirAll(packageDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, source := range sources {
		file := filepath.Join(packageDir, source.name)
		writeTestFile(t, file, source.body)
		files = append(files, file)
	}
	probe := filepath.Join(root, "Probe.java")
	writeTestFile(t, probe, `import reference.patterns.*;
class Probe {
    public static void main(String[] args) {
        var test = new Patterns("seed");
        System.out.println(test.setup());
        System.out.println(test.both("match"));
        System.out.println(test.both(null));
        System.out.println(test.guarded("yes"));
        System.out.println(test.guarded(""));
        System.out.println(test.guarded(4));
        System.out.println(test.elseBinding("else"));
        System.out.println(test.elseBinding(null));
        System.out.println(test.completingThen("initial"));
        System.out.println(test.completingThen(null));
        System.out.println(test.disjoint("one"));
        System.out.println(test.disjoint(new int[]{2, 3}));
        System.out.println(test.ternary("branch"));
        System.out.println(test.ternary(null));
        System.out.println(test.iterations(new Object[]{"a", "long", 4, "not reached"}));
        System.out.println(test.whileExit(null));
        System.out.println(test.doExit(null));
        var child = new Child();
        System.out.println(test.base(child));
        System.out.println(test.base(null));
        System.out.println(test.nominal(child));
        System.out.println(test.nominal(new Lookalike()));
        System.out.println(test.array(test.derivedArray()));
        System.out.println(test.array(new int[1]));
        System.out.println(test.boxed(40));
        System.out.println(test.boxed(null));
    }
}`)
	classes := filepath.Join(root, "classes")
	jdkArguments := append([]string{"--release", strconv.Itoa(release), "-d", classes}, files...)
	jdkArguments = append(jdkArguments, probe)
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"), jdkArguments...)
	want := runObjectTestCommand(t, root, java, "-cp", classes, "Probe")
	report, err := Analyze(t.Context(), files, Options{Java: java, Release: release, MethodBodies: true})
	if err != nil {
		t.Fatal(err)
	}
	translated, err := EmitGPP(report)
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(root, "patterns.gpp")
	writeTestFile(t, input, translated)
	output := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions([]string{input}, output, compiler.CompileOptions{ModulePath: "example.com/java-patterns"}); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(output, "main.go"), `package main

import (
    "fmt"
    "example.com/java-patterns/gpp/java"
    "example.com/java-patterns/reference/patterns"
)

type reference struct { calls *int; value any }

func (r reference) GppJavaCanonicalObject() any {
    (*r.calls)++
    return r.value
}

func main() {
    test := patterns.NewPatternsWithObject(java.StringFromGo("seed"))
    fmt.Println(test.Setup())
    fmt.Println(test.Both(java.StringFromGo("match")))
    fmt.Println(test.Both(nil))
    fmt.Println(test.Guarded(java.StringFromGo("yes")))
    fmt.Println(test.Guarded(java.StringFromGo("")))
    fmt.Println(test.Guarded(java.BoxInteger(4)))
    fmt.Println(test.ElseBinding(java.StringFromGo("else")))
    fmt.Println(test.ElseBinding(nil))
    fmt.Println(test.CompletingThen(java.StringFromGo("initial")))
    fmt.Println(test.CompletingThen(nil))
    fmt.Println(test.Disjoint(java.StringFromGo("one")))
    fmt.Println(test.Disjoint(java.NewArray[int32](1, []int32{2})))
    fmt.Println(test.Ternary(java.StringFromGo("branch")))
    fmt.Println(test.Ternary(nil))
    input := java.NewArray[any](1, []int32{4})
    java.ArraySet[any](input, 0, java.StringFromGo("a"))
    java.ArraySet[any](input, 1, java.StringFromGo("long"))
    java.ArraySet[any](input, 2, java.BoxInteger(4))
    java.ArraySet[any](input, 3, java.StringFromGo("not reached"))
    fmt.Println(test.Iterations(input))
    fmt.Println(test.WhileExit(nil))
    fmt.Println(test.DoExit(nil))
    child := patterns.NewChild()
    fmt.Println(test.Base(child))
    var typedNil *patterns.Child
    fmt.Println(test.Base(typedNil))
    fmt.Println(test.Nominal(child))
    fmt.Println(test.Nominal(patterns.NewLookalike()))
    fmt.Println(test.Array(test.DerivedArray()))
    fmt.Println(test.Array(java.NewArray[int32](1, []int32{1})))
    fmt.Println(test.Boxed(java.BoxInteger(40)))
    fmt.Println(test.Boxed(nil))
    calls := 0
    if got := test.Base(reference{calls: &calls, value: child}); got != 7 || calls != 1 {
        panic(fmt.Sprintf("Go reference callback: result=%d calls=%d", got, calls))
    }
}
`)
	if got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", "."); got != want {
		t.Fatalf("GPP and Java reference patterns diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}

func TestJavaPatternOutOfScopeUsesAreRejected(t *testing.T) {
	java, release := referenceJDK(t)
	source := filepath.Join(t.TempDir(), "OutOfScope.java")
	writeTestFile(t, source, `package reference.patterns;
public class OutOfScope {
    public int either(Object value) {
        if (value instanceof String text || text.length() > 1) return text.length();
        return 0;
    }
}`)
	report, err := Analyze(t.Context(), []string{source}, Options{Java: java, Release: release, MethodBodies: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Kind == "ERROR" && diagnostic.Line == 4 {
			if _, err := EmitGPP(report); err != nil {
				return
			}
			t.Fatal("GPP emission accepted an out-of-scope Java pattern")
		}
	}
	t.Fatalf("expected javac to reject out-of-scope references, got %v", report.Diagnostics)
}
