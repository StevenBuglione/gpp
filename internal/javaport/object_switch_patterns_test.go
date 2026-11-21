package javaport

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestJavaGuardedSwitchPatternsMatchOpenJDK(t *testing.T) {
	java, release := referenceJDK(t)
	if release < 22 {
		t.Skip("stable grouped unnamed patterns require Java 22 or newer")
	}
	root := t.TempDir()
	pkg := filepath.Join(root, "reference", "guarded")
	if err := os.MkdirAll(pkg, 0755); err != nil {
		t.Fatal(err)
	}
	sources := []struct {
		name, code string
	}{
		{"Value.java", `package reference.guarded;
public interface Value { int score(); }`},
		{"Alt.java", `package reference.guarded;
public interface Alt { int other(); }`},
		{"Both.java", `package reference.guarded;
public class Both implements Value, Alt {
    public int score() { return 4; }
    public int other() { return 6; }
}`},
		{"SwitchPatterns.java", `package reference.guarded;
public class SwitchPatterns {
    private int trace;

    private Object selector(Object input) {
        trace = trace * 10 + 1;
        return input;
    }

    private boolean guard(String input) {
        trace = trace * 10 + 2;
        if (input.length() == 4) throw new IllegalArgumentException();
        return input.length() > 4;
    }

    private Boolean boxedGuard(String input) {
        trace = trace * 10 + 3;
        if (input.length() == 3) return null;
        return input.length() == 2;
    }

    public int ordered(Object input, Object extra) {
        trace = 0;
        int result;
        try {
            result = switch (selector(input)) {
                case String text when guard(text) -> text.length();
                case String text when boxedGuard(text) -> text.length() + 10;
                case String text when extra instanceof Integer value && value == 8 ->
                    text.length() + value;
                case String text -> text.length() + 20;
                case Value v -> v.score() + 30;
                case int[] nums -> nums.length + 40;
                case null, default -> 50;
            };
        } catch (IllegalArgumentException e) {
            result = 60;
        } catch (NullPointerException e) {
            result = 70;
        } finally {
            trace = trace * 10 + 4;
        }
        return result * 10000 + trace;
    }

    private boolean groupedGuard() { return ++trace == 2; }

    public int grouped(Object input) {
        trace = 0;
        int result = switch (input) {
            case Value _, Alt _ when groupedGuard() -> 10;
            case null -> 20;
            default -> 30;
        };
        return result + trace;
    }

    public int colon(Object input) {
        int result = 2;
        selected: switch (input) {
            case String text:
                try { result = text.length(); break selected; }
                finally { result += 10; }
            case Integer n: return n;
            case null: return 0;
            default: result = 30;
        }
        return result;
    }

    public int mixed(String input) {
        return switch (input) {
            case "ex" + 'a' + "ct" -> 1;
            case String text when ("[" + text + "]").length() > 4 -> 2;
            case String text -> 3;
            case null -> 4;
        };
    }

    public int exhaustive(Value input) {
        try {
            return switch (input) { case Value v -> v.score(); };
        } catch (NullPointerException e) { return -1; }
          catch (MatchException e) { return -2; }
    }

    public int goGuard(Object input) {
        try {
            return switch (input) {
                case Value v when v.score() > 0 -> v.score();
                case null, default -> -1;
            };
        } catch (RuntimeException failure) { return -2; }
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
	writeTestFile(t, probe, `import reference.guarded.*;
class Probe {
    public static void main(String[] args) {
        var test = new SwitchPatterns();
        var both = new Both();
        for (Object value : new Object[]{"longer", "two", "ok", "four", "a", both, new int[2], null, 1})
            System.out.println(test.ordered(value, 8));
        System.out.println(test.ordered("a", null));
        System.out.println(test.grouped(both));
        System.out.println(test.grouped(null));
        System.out.println(test.colon("word"));
        System.out.println(test.colon(8));
        System.out.println(test.colon(null));
        System.out.println(test.colon(both));
        System.out.println(test.mixed("exact"));
        System.out.println(test.mixed("other"));
        System.out.println(test.mixed("hi"));
        System.out.println(test.mixed(null));
        System.out.println(test.exhaustive(both));
        System.out.println(test.exhaustive(null));
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
	if err := compiler.CompileFilesWithOptions([]string{input}, output, compiler.CompileOptions{ModulePath: "example.com/guardedswitch"}); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(output, "main.go"), `package main
import (
    "fmt"
    "example.com/guardedswitch/gpp/java"
    "example.com/guardedswitch/reference/guarded"
)
type nominalMismatch struct{}
func (nominalMismatch) Score() int32 { return 9 }
func (nominalMismatch) GppJavaImplementsInterface(string) bool { return false }
type callback struct { token any }
func (c callback) Score() int32 { panic(c.token) }
func main() {
    test := guarded.NewSwitchPatterns()
    both := guarded.NewBoth()
    for _, value := range []any{java.StringFromGo("longer"), java.StringFromGo("two"), java.StringFromGo("ok"),
        java.StringFromGo("four"), java.StringFromGo("a"), both, java.NewArray[int32](1, []int32{2}),
        (*java.String)(nil), java.BoxInteger(1)} {
        fmt.Println(test.Ordered(value, java.BoxInteger(8)))
    }
    fmt.Println(test.Ordered(java.StringFromGo("a"), nil))
    fmt.Println(test.Grouped(both))
    fmt.Println(test.Grouped(nil))
    fmt.Println(test.Colon(java.StringFromGo("word")))
    fmt.Println(test.Colon(java.BoxInteger(8)))
    fmt.Println(test.Colon(nil))
    fmt.Println(test.Colon(both))
    fmt.Println(test.Mixed(java.StringFromGo("exact")))
    fmt.Println(test.Mixed(java.StringFromGo("other")))
    fmt.Println(test.Mixed(java.StringFromGo("hi")))
    fmt.Println(test.Mixed(nil))
    fmt.Println(test.Exhaustive(both))
    fmt.Println(test.Exhaustive(nil))
    if test.Exhaustive(nominalMismatch{}) != -2 { panic("missing MatchException") }
    func() {
        token := new(int)
        defer func() {
            if recovered := recover(); recovered != token { panic("switch guard swallowed a native Go panic") }
        }()
        _ = test.GoGuard(callback{token})
    }()
}
`)
	if got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", "."); got != want {
		t.Fatalf("GPP and Java guarded switches diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}
