package javaport

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestJavaRecordSwitchPatternsMatchOpenJDK(t *testing.T) {
	java, release := referenceJDK(t)
	if release < 22 {
		t.Skip("stable unnamed record patterns require Java 22 or newer")
	}
	root := t.TempDir()
	pkg := filepath.Join(root, "reference", "recordpatterns")
	if err := os.MkdirAll(pkg, 0755); err != nil {
		t.Fatal(err)
	}
	sources := []struct {
		name, code string
	}{
		{"Inner.java", `package reference.recordpatterns;
public record Inner(int number) {
    public int number() {
        Outer.markNumber();
        if (number < 0) throw new IllegalArgumentException("negative");
        return number;
    }
}`},
		{"Outer.java", `package reference.recordpatterns;
public record Outer(Inner inside, String text) {
    private static int access;
    public Inner inside() { access = access * 10 + 1; return inside; }
    public String text() { access = access * 10 + 2; return text; }
    public static void markNumber() { access = access * 10 + 3; }
    public int order() { return access; }
    public void reset() { access = 0; }
}`},
		{"Plain.java", `package reference.recordpatterns;
public record Plain(int number, String text) {}`},
		{"RecordPatterns.java", `package reference.recordpatterns;
public class RecordPatterns {
    public int guarded(Outer value) {
        value.reset();
        int result = switch (value) {
            case Outer(Inner(int count), String text) when text == null -> count + 100;
            case Outer matching -> {
                var inside = matching.inside();
                yield inside == null ? 20 : inside.number() + matching.text().length();
            }
        };
        return result * 100000 + value.order();
    }

    public int unmatchedComponentStopsAccessors(Outer value) {
        value.reset();
        int result = switch (value) {
            case Outer(Inner(_), _) -> 1;
            default -> 2;
        };
        return result * 100 + value.order();
    }

    public int repeated(Plain value) {
        return switch (value) {
            case Plain(int n, String text) when text == null -> n + 100;
            case Plain(var n, var text) -> n + text.length();
        };
    }

    public int instanceCheck(Object input, Outer monitor) {
        monitor.reset();
        if (!(input instanceof Outer(Inner(int count), String text))) return -monitor.order();
        return (count + (text == null ? 10 : text.length())) * 1000 + monitor.order();
    }

    public int exhaustiveExpression(Outer value) {
        try {
            return switch (value) { case Outer(Inner(var count), var text) -> count; };
        } catch (MatchException failure) {
            return failure.getCause() == null ? -2 : -3;
        } catch (NullPointerException failure) {
            return -1;
        }
    }

    public int exhaustiveStatement(Outer value) {
        try {
            int result = 0;
            switch (value) { case Outer(Inner(var count), var text) -> result = count; }
            return result;
        } catch (MatchException failure) {
            return failure.getCause() == null ? -2 : -3;
        } catch (NullPointerException failure) {
            return -1;
        }
    }

    public int cause(Outer value) {
        try {
            return switch (value) { case Outer(Inner(int n), _) -> n; default -> 0; };
        } catch (MatchException failure) {
            if (failure.getCause() instanceof IllegalArgumentException cause &&
                failure.getMessage().equals("java.lang.IllegalArgumentException: negative") &&
                cause.getMessage().equals("negative")) return 8;
            return -3;
        }
    }
}`},
	}
	files := make([]string, 0, len(sources))
	for _, item := range sources {
		path := filepath.Join(pkg, item.name)
		writeTestFile(t, path, item.code)
		files = append(files, path)
	}
	probe := filepath.Join(root, "Probe.java")
	writeTestFile(t, probe, `import reference.recordpatterns.*;
class Probe {
    public static void main(String[] args) {
        var test = new RecordPatterns();
        var matched = new Outer(new Inner(7), "abc");
        var nullText = new Outer(new Inner(5), null);
        var nullInside = new Outer(null, "abc");
        var thrown = new Outer(new Inner(-1), "unused");
        System.out.println(test.guarded(matched));
        System.out.println(test.guarded(nullText));
        System.out.println(test.guarded(nullInside));
        System.out.println(test.unmatchedComponentStopsAccessors(matched));
        System.out.println(test.unmatchedComponentStopsAccessors(nullInside));
        System.out.println(test.repeated(new Plain(7, "abc")));
        System.out.println(test.repeated(new Plain(5, null)));
        System.out.println(test.instanceCheck(matched, matched));
        System.out.println(test.instanceCheck(nullText, nullText));
        System.out.println(test.instanceCheck(nullInside, nullInside));
        System.out.println(test.instanceCheck(null, matched));
        System.out.println(test.exhaustiveExpression(matched));
        System.out.println(test.exhaustiveExpression(nullInside));
        System.out.println(test.exhaustiveExpression(null));
        System.out.println(test.exhaustiveStatement(matched));
        System.out.println(test.exhaustiveStatement(nullInside));
        System.out.println(test.exhaustiveStatement(null));
        System.out.println(test.cause(thrown));
    }
}`)
	classes := filepath.Join(root, "classes")
	args := append([]string{"--release", strconv.Itoa(release), "-d", classes}, files...)
	args = append(args, probe)
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"), args...)
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
	if err := compiler.CompileFilesWithOptions([]string{input}, output, compiler.CompileOptions{ModulePath: "example.com/recordpatterns"}); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(output, "main.go"), `package main
import (
    "fmt"
    "example.com/recordpatterns/gpp/java"
    "example.com/recordpatterns/reference/recordpatterns"
)
func main() {
    test := recordpatterns.NewRecordPatterns()
    matched := recordpatterns.NewOuterWithInnerAndString(recordpatterns.NewInnerWithInt(7), java.StringFromGo("abc"))
    nullText := recordpatterns.NewOuterWithInnerAndString(recordpatterns.NewInnerWithInt(5), nil)
    nullInside := recordpatterns.NewOuterWithInnerAndString(nil, java.StringFromGo("abc"))
    thrown := recordpatterns.NewOuterWithInnerAndString(recordpatterns.NewInnerWithInt(-1), java.StringFromGo("unused"))
    fmt.Println(test.Guarded(matched))
    fmt.Println(test.Guarded(nullText))
    fmt.Println(test.Guarded(nullInside))
    fmt.Println(test.UnmatchedComponentStopsAccessors(matched))
    fmt.Println(test.UnmatchedComponentStopsAccessors(nullInside))
    fmt.Println(test.Repeated(recordpatterns.NewPlainWithIntAndString(7, java.StringFromGo("abc"))))
    fmt.Println(test.Repeated(recordpatterns.NewPlainWithIntAndString(5, nil)))
    fmt.Println(test.InstanceCheck(matched, matched))
    fmt.Println(test.InstanceCheck(nullText, nullText))
    fmt.Println(test.InstanceCheck(nullInside, nullInside))
    fmt.Println(test.InstanceCheck(nil, matched))
    fmt.Println(test.ExhaustiveExpression(matched))
    fmt.Println(test.ExhaustiveExpression(nullInside))
    fmt.Println(test.ExhaustiveExpression(nil))
    fmt.Println(test.ExhaustiveStatement(matched))
    fmt.Println(test.ExhaustiveStatement(nullInside))
    fmt.Println(test.ExhaustiveStatement(nil))
    fmt.Println(test.Cause(thrown))
    func() {
        token := new(int)
        defer func() {
            if recovered := recover(); recovered != token { panic("deconstruction swallowed a native Go panic") }
        }()
        _ = java.DeconstructRecordComponent(func() int32 { panic(token) })
    }()
}
`)
	if got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", "."); got != want {
		t.Fatalf("GPP and Java record patterns diverged\nGo:\n%s\nJava:\n%s", got, want)
	}

	// Verify that we reject a shared-prefix case that would silently change
	// the number of calls to a user-defined accessor if we emitted it.
	shared := filepath.Join(pkg, "Shared.java")
	writeTestFile(t, shared, `package reference.recordpatterns;
public class Shared {
    public int unsupported(Outer value) {
        return switch (value) {
            case Outer(Inner(var n), String text) when text == null -> n;
            case Outer(Inner(var n), var text) -> n + 1;
        };
    }
}`)
	report, err = Analyze(t.Context(), append(files, shared), Options{Java: java, Release: release, MethodBodies: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EmitGPP(report); err == nil || !strings.Contains(err.Error(), "unsupported shared accessors across consecutive Java record patterns") {
		t.Fatalf("expected a shared record accessor diagnostic, got %v", err)
	}
}
