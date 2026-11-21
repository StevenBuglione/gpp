package javaport

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestNativeLabeledBlocksMatchJava(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	reference := filepath.Join(root, "Probe.java")
	writeTestFile(t, reference, `class Probe {
    static final int Number;
    static {
        Assign: {
            if (choose()) {
                Number = 23;
                break Assign;
            }
            Number = 24;
        }
    }

    static boolean choose() { return true; }

    static String trace() {
        String result = "";
        Exit: {
            try {
                Local: {
                    result += "L";
                    if (choose()) break Local;
                    result += "X";
                }
                result += "T";
                if (choose()) break Exit;
            } finally {
                result += "F";
            }
            result += "X";
        }
        Exit: {
            result += "D";
            if (choose()) break Exit;
            result += "X";
        }
        Outer: Inner: {
            result += "C";
            if (choose()) break Outer;
            result += "X";
        }
        Unused: { result += "U"; }
        for (int i = 0; i < 3; i++) {
            try {
                Scope: {
                    if (i == 0) continue;
                    if (i == 2) break;
                    if (choose()) break Scope;
                    result += "X";
                }
            } finally {
                result += "Y";
            }
            result += "N";
        }
        return result;
    }

    static int finallyExit() {
        Exit: {
            try {
                return 1;
            } finally {
                if (choose()) break Exit;
            }
        }
        return 2;
    }

    public static void main(String[] args) {
        System.out.println(Number);
        System.out.println(trace());
        System.out.println(finallyExit());
    }
}`)
	classes := filepath.Join(root, "classes")
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"),
		"--release", strconv.Itoa(release), "-d", classes, reference)
	want := runObjectTestCommand(t, root, java, "-cp", classes, "Probe")

	source := filepath.Join(root, "main.gpp")
	writeTestFile(t, source, `package main
import "fmt"

class Probe {
    static final Number int
    static {
        Assign: {
            if choose() {
                Number = 23
                break Assign
            }
            Number = 24
        }
    }
}

func choose() bool { return true }

func trace() string {
    result := ""
    Exit: {
        try {
            Local: {
                result += "L"
                if choose() { break Local }
                result += "X"
            }
            result += "T"
            if choose() { break Exit }
        } finally {
            result += "F"
        }
        result += "X"
    }
    Exit: {
        result += "D"
        if choose() { break Exit }
        result += "X"
    }
    Outer: Inner: {
        result += "C"
        if choose() { break Outer }
        result += "X"
    }
    Unused: { result += "U" }
    for i := 0; i < 3; i++ {
        try {
            Scope: {
                if i == 0 { continue }
                if i == 2 { break }
                if choose() { break Scope }
                result += "X"
            }
        } finally {
            result += "Y"
        }
        result += "N"
    }
    return result
}

func finallyExit() int {
    Exit: {
        try {
            return 1
        } finally {
            if choose() { break Exit }
        }
    }
    return 2
}

func goBlockEntry() int {
    visits := 0
    Again: {
        visits++
        if visits == 1 { goto Again }
        if visits == 2 { break Again }
        visits += 10
    }
    return visits
}

func main() {
    if goBlockEntry() != 2 { panic("Go goto no longer targets the block entry") }
    fmt.Println(Probe.Number)
    fmt.Println(trace())
    fmt.Println(finallyExit())
}`)
	output := filepath.Join(root, "output")
	if err := compiler.CompileFilesWithOptions([]string{source}, output,
		compiler.CompileOptions{ModulePath: "example.com/labeled-blocks"}); err != nil {
		t.Fatal(err)
	}
	if got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", "."); got != want {
		t.Fatalf("GPP and Java labeled blocks diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}

func TestNativeLabeledBlockDiagnosticsMatchJava(t *testing.T) {
	java, release := referenceJDK(t)
	for _, test := range []struct {
		name, java, gpp, javaDiagnostic, gppDiagnostic string
	}{
		{"duplicate inside try", "Exit: { try { Exit: { break Exit; } } finally {} }",
			"Exit: { try { Exit: { break Exit } } finally {} }",
			"label Exit already in use", "duplicate enclosing label Exit"},
		{"continue to a block", "Exit: { continue Exit; }", "Exit: { continue Exit }",
			"not a loop label: Exit", "continue requires a loop"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			reference := filepath.Join(root, "Probe.java")
			writeTestFile(t, reference, "class Probe { static void test() { "+test.java+" } }")
			command := exec.Command(filepath.Join(filepath.Dir(java), "javac"), "--release", strconv.Itoa(release), reference)
			if output, err := command.CombinedOutput(); err == nil || !strings.Contains(string(output), test.javaDiagnostic) {
				t.Fatalf("unexpected javac result: %v\n%s", err, output)
			}
			source := filepath.Join(root, "main.gpp")
			writeTestFile(t, source, "package main\nfunc test() { "+test.gpp+" }\n")
			if err := compiler.CompileFilesWithOptions([]string{source}, filepath.Join(root, "output"),
				compiler.CompileOptions{ModulePath: "example.com/labeled-block-errors"}); err == nil || !strings.Contains(err.Error(), test.gppDiagnostic) {
				t.Fatalf("expected GPP diagnostic %q, got %v", test.gppDiagnostic, err)
			}
		})
	}
}

func TestNativeLabeledBlocksRejectAmbiguousGoGoto(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "main.gpp")
	writeTestFile(t, source, `package main
func test() {
    goto Entry
    Entry: {}
    Entry: {}
}`)
	err := compiler.CompileFilesWithOptions([]string{source}, filepath.Join(root, "output"),
		compiler.CompileOptions{ModulePath: "example.com/ambiguous-goto"})
	if err == nil || !strings.Contains(err.Error(), "ambiguous goto label Entry") {
		t.Fatalf("expected an ambiguous goto diagnostic, got %v", err)
	}
}

func TestNativeLabeledBlocksRequireFinalAssignment(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	reference := filepath.Join(root, "Probe.java")
	writeTestFile(t, reference, `class Probe {
    static final int Value;
    static boolean choose() { return true; }
    static { Exit: { if (choose()) break Exit; Value = 1; } }
}`)
	command := exec.Command(filepath.Join(filepath.Dir(java), "javac"), "--release", strconv.Itoa(release), reference)
	output, javacErr := command.CombinedOutput()
	message := string(output)
	if javacErr == nil || !(strings.Contains(message, "variable Value might not have been initialized") ||
		strings.Contains(message, "variable Value not initialized in the default constructor")) {
		t.Fatalf("unexpected javac result: %v\n%s", javacErr, output)
	}
	source := filepath.Join(root, "main.gpp")
	writeTestFile(t, source, `package main
func choose() bool { return true }
class Probe {
    static final Value int
    static { Exit: { if choose() { break Exit }; Value = 1 } }
}`)
	err := compiler.CompileFilesWithOptions([]string{source}, filepath.Join(root, "output"),
		compiler.CompileOptions{ModulePath: "example.com/labeled-block-final"})
	if err == nil || !strings.Contains(err.Error(), "static final field Value might not be initialized") {
		t.Fatalf("expected a blank final diagnostic, got %v", err)
	}
}
