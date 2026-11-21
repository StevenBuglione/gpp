package javaport

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestJavaSwitchesMatchOpenJDK(t *testing.T) {
	java, release := referenceJDK(t)
	if release < 21 {
		t.Skip("null switch labels and combined null/default require Java 21 or newer")
	}
	root := t.TempDir()
	source := filepath.Join(root, "Switches.java")
	writeTestFile(t, source, `package reference.switches;

public class Switches {
    private int trace;

    private int mark(int digit) {
        trace = trace * 10 + digit;
        return digit;
    }

    public int classic(int selector) {
        trace = 0;
        switch (mark(selector)) {
            case 1:
                int across = mark(2);
            case 3:
                across = mark(4);
                mark(across);
                break;
            default:
                mark(5);
            case 6:
            case 7:
                mark(8);
        }
        return trace;
    }

    public int rules(int selector) {
        trace = 0;
        switch (mark(selector)) {
            case 1, 2 -> mark(4);
            case 3 -> { mark(5); mark(6); }
        }
        switch (mark(7)) {}
        return trace;
    }

    public int groupsYield(int selector) {
        trace = 0;
        int picked = switch (selector) {
            case 0:
                int across = mark(1);
            case 1:
                across = mark(2);
                yield across;
            default:
                yield mark(3);
        };
        return picked * 100 + trace;
    }

    public int control() {
        trace = 0;
        Loop: for (int index = 0; index != 3; index++, mark(7)) {
            Selection: switch (index) {
                case 0:
                    try {
                        continue;
                    } finally {
                        mark(1);
                    }
                case 1:
                    try {
                        break Selection;
                    } finally {
                        mark(2);
                    }
                default:
                    try {
                        break;
                    } finally {
                        mark(3);
                    }
            }
            mark(4);
        }
        return trace;
    }

    public int boxed(Byte number) {
        return switch (number) {
            case -128 -> 1;
            case 127 -> 2;
            case null -> 3;
            default -> 4;
        };
    }

    public int combined(Object value) {
        return switch (value) { case null, default -> 11; };
    }

    public int text(String value) {
        try {
            return switch (value) {
                case "FB" -> 1;
                case "Ea", "T" -> 2;
                default -> 3;
            };
        } catch (NullPointerException error) {
            return 4;
        }
    }

    public int throughYield(int mode, AutoCloseable resource) throws Exception {
        trace = 0;
        int value;
        try {
            value = switch (mode) {
                case 0 -> {
                    try { yield mark(1); }
                    finally { mark(2); yield mark(3); }
                }
                case 1 -> {
                    try (resource) { yield mark(4); }
                    catch (IllegalArgumentException error) { yield mark(5); }
                    finally { mark(6); }
                }
                case 2 -> {
                    try { throw new IllegalArgumentException(); }
                    finally { yield mark(7); }
                }
                case 3 -> {
                    try { yield mark(8); }
                    finally { throw new IllegalArgumentException(); }
                }
                case 4 -> {
                    try (resource) { yield mark(4); }
                    finally { yield mark(6); }
                }
                default -> {
                    yield switch (mark(9)) {
                        case 9 -> {
                            try { yield mark(2); }
                            finally { mark(3); }
                        }
                        default -> 4;
                    };
                }
            };
        } catch (IllegalArgumentException error) {
            value = mark(1);
        }
        return value * 1000 + trace;
    }

    public int armReturn(int selector) {
        switch (selector) {
            case 1 -> { try { return 2; } finally { return 3; } }
            default -> { return 4; }
        }
    }
}`)
	probe := filepath.Join(root, "Probe.java")
	writeTestFile(t, probe, `import reference.switches.Switches;

class Probe {
    public static void main(String[] args) throws Exception {
        var test = new Switches();
        for (int index = 0; index < 8; index++) System.out.println(test.classic(index));
        for (int index = 0; index < 5; index++) System.out.println(test.rules(index));
        for (int index = 0; index < 3; index++) System.out.println(test.groupsYield(index));
        System.out.println(test.control());
        System.out.println(test.boxed((byte)-128));
        System.out.println(test.boxed((byte)127));
        System.out.println(test.boxed(null));
        System.out.println(test.boxed((byte)1));
        System.out.println(test.combined(null));
        System.out.println(test.combined(new Object()));
        System.out.println(test.text(new String("FB")));
        System.out.println(test.text(new String("Ea")));
        System.out.println(test.text("T"));
        System.out.println(test.text("not matched"));
        System.out.println(test.text(null));
        int[] closes = {0};
        for (int mode = 0; mode < 6; mode++)
            System.out.println(test.throughYield(mode, () -> closes[0]++) + " " + closes[0]);
        System.out.println(test.throughYield(1, () -> { closes[0]++; throw new IllegalArgumentException(); }) + " " + closes[0]);
        System.out.println(test.armReturn(1));
        System.out.println(test.armReturn(0));
    }
}`)
	classes := filepath.Join(root, "classes")
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"), "--release", strconv.Itoa(release), "-d", classes, source, probe)
	want := runObjectTestCommand(t, root, java, "-cp", classes, "Probe")
	report, err := Analyze(t.Context(), []string{source}, Options{Java: java, Release: release, MethodBodies: true})
	if err != nil {
		t.Fatal(err)
	}
	translated, err := EmitGPP(report)
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(root, "switches.gpp")
	writeTestFile(t, input, translated)
	output := filepath.Join(root, "output")
	if err := compiler.CompileFilesWithOptions([]string{input}, output, compiler.CompileOptions{ModulePath: "example.com/java-switches"}); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(output, "main.go"), `package main

import (
    "fmt"
    "example.com/java-switches/gpp/java"
    "example.com/java-switches/reference/switches"
)

type resource struct { closes *int; failure bool; raw any }

func (*resource) GppAutoCloseable() {}
func (r *resource) Close() {
    (*r.closes)++
    if r.failure { java.Raise(java.NewIllegalArgumentException(nil)) }
    if r.raw != nil { panic(r.raw) }
}

func throughYield(test *switches.Switches, mode int32, closer *resource) int32 {
    value, err := test.GppCallThroughYield(mode, closer)
    if err != nil { panic(err) }
    return value
}

func main() {
    test := switches.NewSwitches()
    for index := int32(0); index < 8; index++ { fmt.Println(test.Classic(index)) }
    for index := int32(0); index < 5; index++ { fmt.Println(test.Rules(index)) }
    for index := int32(0); index < 3; index++ { fmt.Println(test.GroupsYield(index)) }
    fmt.Println(test.Control())
    fmt.Println(test.Boxed(java.BoxByte(-128)))
    fmt.Println(test.Boxed(java.BoxByte(127)))
    fmt.Println(test.Boxed(nil))
    fmt.Println(test.Boxed(java.BoxByte(1)))
    var typedNil *switches.Switches
    fmt.Println(test.Combined(typedNil))
    fmt.Println(test.Combined(test))
    fmt.Println(test.Text(java.StringFromGo("FB")))
    fmt.Println(test.Text(java.StringFromGo("Ea")))
    fmt.Println(test.Text(java.StringFromGo("T")))
    fmt.Println(test.Text(java.StringFromGo("not matched")))
    fmt.Println(test.Text(nil))
    closes := 0
    for mode := int32(0); mode < 6; mode++ {
        result := throughYield(test, mode, &resource{closes: &closes})
        fmt.Println(result, closes)
    }
    result := throughYield(test, 1, &resource{closes: &closes, failure: true})
    fmt.Println(result, closes)
    fmt.Println(test.ArmReturn(1))
    fmt.Println(test.ArmReturn(0))
    func() {
        token := new(int)
        defer func() {
            if recovered := recover(); recovered != token { panic("yield in finally hid native Go panic") }
        }()
        _ = throughYield(test, 4, &resource{closes: &closes, raw: token})
    }()
}
`)
	if got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", "."); got != want {
		t.Fatalf("GPP and Java switch behavior diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}

func TestJavaSwitchLabelsInSupportedAnalyzers(t *testing.T) {
	java, release := referenceJDK(t)
	t.Run("constant and default labels", func(t *testing.T) {
		source := filepath.Join(t.TempDir(), "Switches.java")
		writeTestFile(t, source, `package reference.switches;
public class Switches {
    public int first(int key) { switch (key) { case 1: case 2: return 4; default: return 5; } }
    public int second(int key) { return switch (key) { case 1, 2 -> 4; default -> 5; }; }
}`)
		report, err := Analyze(t.Context(), []string{source}, Options{Java: java, Release: release, MethodBodies: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := EmitGPP(report); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("pattern labels report an unsupported feature", func(t *testing.T) {
		if release < 21 {
			t.Skip("stable pattern labels require Java 21 or newer")
		}
		source := filepath.Join(t.TempDir(), "Switches.java")
		writeTestFile(t, source, `package reference.switches;
public class Switches {
    public int length(Object key) {
        return switch (key) {
            case String s when s.length() != 0 -> s.length();
            default -> 0;
        };
    }
}`)
		report, err := Analyze(t.Context(), []string{source}, Options{Java: java, Release: release, MethodBodies: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := EmitGPP(report); err == nil || !strings.Contains(err.Error(), "unsupported Java switch pattern label") {
			t.Fatalf("expected a source-located unsupported pattern diagnostic, got %v", err)
		}
	})
}
