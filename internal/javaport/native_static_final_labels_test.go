package javaport

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestNativeStaticFinalLabeledInitializationMatchesJava(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	reference := filepath.Join(root, "Probe.java")
	writeTestFile(t, reference, `class Probe {
    static final int BreakValue;
    static {
        OuterBreak: for (;;) {
            for (;;) {
                BreakValue = 17;
                break OuterBreak;
            }
        }
    }

    static final int ContinueValue;
    static {
        OuterContinue: for (int index = 0;; index++) {
            for (;;) {
                if (index == 0) continue OuterContinue;
                ContinueValue = 29 + index;
                break OuterContinue;
            }
        }
    }

    static int choose() { return 1; }
    static final int SwitchValue;
    static {
        Selection: switch (choose()) {
            case 1:
                for (;;) {
                    SwitchValue = 41;
                    break Selection;
                }
            default:
                SwitchValue = 42;
        }
    }

    static final int TryValue;
    static {
        try {
            Nested: for (;;) {
                for (;;) {
                    TryValue = 53;
                    break Nested;
                }
            }
        } finally {}
    }

    public static void main(String[] args) {
        System.out.println(BreakValue + " " + ContinueValue + " " + SwitchValue + " " + TryValue);
    }
}`)
	classes := filepath.Join(root, "classes")
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"),
		"--release", strconv.Itoa(release), "-d", classes, reference)
	want := runObjectTestCommand(t, root, java, "-cp", classes, "Probe")

	source := filepath.Join(root, "main.gpp")
	writeTestFile(t, source, `package main
import "fmt"

func choose() int { return 1 }

class Probe {
    static final BreakValue int
    static {
        OuterBreak: for {
            for {
                BreakValue = 17
                break OuterBreak
            }
        }
    }

    static final ContinueValue int
    static {
        OuterContinue:
        for index := 0; ; index++ {
            for {
                if index == 0 { continue OuterContinue }
                ContinueValue = 29 + index
                break OuterContinue
            }
        }
    }

    static final SwitchValue int
    static {
        Selection: switch choose() {
            case 1:
                for {
                    SwitchValue = 41
                    break Selection
                }
            default:
                SwitchValue = 42
        }
    }

    static final TryValue int
    static {
        try {
            Nested: for {
                for {
                    TryValue = 53
                    break Nested
                }
            }
        } finally {}
    }
}

func ordinaryLabels() int {
    Outer: for index := 0; ; index++ {
        for {
            if index == 0 { continue Outer }
            goto Done
        }
    }
    Done: return 1
}

func main() {
    if ordinaryLabels() != 1 { panic("ordinary Go labels stopped working") }
    fmt.Println(Probe.BreakValue, Probe.ContinueValue, Probe.SwitchValue, Probe.TryValue)
}`)
	output := filepath.Join(root, "output")
	if err := compiler.CompileFilesWithOptions([]string{source}, output,
		compiler.CompileOptions{ModulePath: "example.com/labeled-static-final"}); err != nil {
		t.Fatal(err)
	}
	if got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", "."); got != want {
		t.Fatalf("GPP and Java labeled initialization diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}

func TestNativeStaticFinalLabeledInitializationDiagnosticsMatchJava(t *testing.T) {
	java, release := referenceJDK(t)
	cases := []struct {
		name, java, gpp, javaDiagnostic, gppDiagnostic string
	}{
		{
			"break before assignment",
			"Outer: for (;;) { for (;;) { if (choose()) break Outer; Value = 1; break Outer; } }",
			"Outer: for { for { if choose() { break Outer }; Value = 1; break Outer } }",
			`variable Value (might not have been|not) initialized`,
			"might not be initialized",
		},
		{
			"continue after assignment",
			"Outer: for (;;) { for (;;) { Value = 1; if (choose()) continue Outer; break Outer; } }",
			"Outer: for { for { Value = 1; if choose() { continue Outer }; break Outer } }",
			"variable Value might already have been assigned",
			"might already be assigned",
		},
		{
			"break to unknown label",
			"for (;;) { Value = 1; break Unknown; }",
			"for { Value = 1; break Unknown }",
			"undefined label: Unknown",
			"invalid break target Unknown",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			reference := filepath.Join(root, "Probe.java")
			writeTestFile(t, reference, "class Probe { static final int Value; static boolean choose() { return true; } static { "+test.java+" } }")
			javac := exec.CommandContext(t.Context(), filepath.Join(filepath.Dir(java), "javac"),
				"--release", strconv.Itoa(release), "-d", root, reference)
			if output, err := javac.CombinedOutput(); err == nil || !regexp.MustCompile(test.javaDiagnostic).Match(output) {
				t.Fatalf("expected javac diagnostic %q, got %v\n%s", test.javaDiagnostic, err, output)
			}

			source := "package main\nfunc choose() bool { return true }\nclass Probe {\nstatic final Value int\nstatic { " + test.gpp + " }\n}"
			file, err := compiler.ParseFile("probe.gpp", source)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := compiler.Emit(file); err == nil || !strings.Contains(err.Error(), test.gppDiagnostic) {
				t.Fatalf("expected %q, got %v", test.gppDiagnostic, err)
			}
		})
	}
}
