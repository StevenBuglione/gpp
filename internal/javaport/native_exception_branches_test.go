package javaport

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestNativeExceptionBranchesMatchJava(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	reference := filepath.Join(root, "Probe.java")
	writeTestFile(t, reference, `class Probe {
    static final int Value;
    static int Cleanups;
    static {
        Outer: for (;;) {
            try {
                Value = 31;
                break Outer;
            } catch (RuntimeException error) {
                throw error;
            }
        }
    }
    static final int AfterContinue;
    static {
        Continuation: for (int index = 0;; index++) {
            try {
                if (index == 0) continue Continuation;
            } finally {
                Cleanups++;
            }
            AfterContinue = 40 + index;
            break Continuation;
        }
    }

    static String trace(int mode) {
        String result = "";
        Outer: for (int index = 0; index < 2; index++) {
            for (int inner = 0; inner < 1; inner++) {
                try {
                    try {
                        result += "T";
                        if (mode == 0) continue Outer;
                        if (mode == 1) break Outer;
                        if (mode == 2 || mode == 5 || mode == 7) throw new IllegalArgumentException();
                        if (mode == 3) break;
                        if (mode == 6) break Outer;
                        Local: for (;;) {
                            result += "L";
                            break Local;
                        }
                        continue;
                    } catch (IllegalArgumentException caught) {
                        result += "C";
                        if (mode == 5) break Outer;
                        if (mode == 7) break;
                        continue Outer;
                    } finally {
                        result += "F";
                        if (mode == 1) throw new IllegalStateException();
                    }
                } catch (IllegalStateException caught) {
                    result += "X";
                } finally {
                    result += "G";
                }
            }
            result += "N";
        }
        return result;
    }

    static String switchExit() {
        String result = "";
        Selection: switch (Value) {
            case 31:
                for (int inner = 0; inner < 2; inner++) {
                    try {
                        result += "S";
                        break Selection;
                    } finally {
                        result += "F";
                    }
                }
                result += "N";
                break;
            default:
                result += "D";
        }
        return result;
    }

    static int caughtExit() {
        Exit: for (;;) {
            try {
                throw new IllegalArgumentException();
            } catch (IllegalArgumentException caught) {
                break Exit;
            }
        }
        return 47;
    }

    public static void main(String[] args) {
        System.out.println(Value + " " + AfterContinue + " " + Cleanups);
        for (int mode = 0; mode <= 7; mode++) System.out.println(trace(mode));
        System.out.println(switchExit());
        System.out.println(caughtExit());
    }
}`)
	classes := filepath.Join(root, "classes")
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"),
		"--release", strconv.Itoa(release), "-d", classes, reference)
	want := runObjectTestCommand(t, root, java, "-cp", classes, "Probe")

	source := filepath.Join(root, "main.gpp")
	writeTestFile(t, source, `package main
import "errors"
import "fmt"

class Probe {
    static final Value int
    static Cleanups int
    static {
        Outer: for {
            try {
                Value = 31
                break Outer
            } catch error caught {
                throw caught
            }
        }
    }
    static final AfterContinue int
    static {
        Continuation: for index := 0; ; index++ {
            try {
                if index == 0 { continue Continuation }
            } finally {
                Cleanups++
            }
            AfterContinue = 40 + index
            break Continuation
        }
    }
}

func trace(mode int) string {
    result := ""
    Outer: for index := 0; index < 2; index++ {
        for inner := 0; inner < 1; inner++ {
            try {
                try {
                    result += "T"
                    if mode == 0 { continue Outer }
                    if mode == 1 { break Outer }
                    if mode == 2 || mode == 5 || mode == 7 { throw errors.New("try") }
                    if mode == 3 { break }
                    if mode == 6 { break Outer }
                    Local: for {
                        result += "L"
                        break Local
                    }
                    continue
                } catch error {
                    result += "C"
                    if mode == 5 { break Outer }
                    if mode == 7 { break }
                    continue Outer
                } finally {
                    result += "F"
                    if mode == 1 { throw errors.New("cleanup") }
                }
            } catch error {
                result += "X"
            } finally {
                result += "G"
            }
        }
        result += "N"
    }
    return result
}

func switchExit() string {
    result := ""
    Selection: switch Probe.Value {
        case 31:
            for inner := 0; inner < 2; inner++ {
                try {
                    result += "S"
                    break Selection
                } finally {
                    result += "F"
                }
            }
            result += "N"
        default:
            result += "D"
    }
    return result
}

func caughtExit() int {
    Exit: for {
        try {
            throw errors.New("branch")
        } catch error {
            break Exit
        }
    }
    return 47
}

func main() {
    fmt.Println(Probe.Value, Probe.AfterContinue, Probe.Cleanups)
    for mode := 0; mode <= 7; mode++ { fmt.Println(trace(mode)) }
    fmt.Println(switchExit())
    fmt.Println(caughtExit())
}`)
	output := filepath.Join(root, "output")
	if err := compiler.CompileFilesWithOptions([]string{source}, output,
		compiler.CompileOptions{ModulePath: "example.com/exception-branches"}); err != nil {
		t.Fatal(err)
	}
	if got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", "."); got != want {
		t.Fatalf("GPP and Java exception branches diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}

func TestNativeExceptionBranchesPreserveGoControl(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "main.gpp")
	writeTestFile(t, source, `package main
import "errors"
import "fmt"

func failLater() { throw errors.New("defer") }

func main() {
    result := ""
    Range: for range 2 {
        try {
            select {
                default:
                    result += "S"
                    break
            }
            func() {
                for {
                    result += "T"
                    break
                }
            }()
            continue Range
        } finally {
            result += "F"
        }
        result += "unreachable"
    }

    __gppTryBranch := "C"
    for range 2 {
        try {
            defer failLater()
            break
        } catch error {
            result += __gppTryBranch
        } finally {
            result += "F"
        }
        result += "N"
    }
    fmt.Println(result)
}`)
	output := filepath.Join(root, "output")
	if err := compiler.CompileFilesWithOptions([]string{source}, output,
		compiler.CompileOptions{ModulePath: "example.com/exception-branches-go"}); err != nil {
		t.Fatal(err)
	}
	if got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", "."); strings.TrimSpace(got) != "STFSTFCFNCFN" {
		t.Fatalf("unexpected native Go execution trace %q", got)
	}
}

func TestNativeExceptionBranchesKeepFinallyTransferDiagnostic(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "main.gpp")
	writeTestFile(t, source, `package main

func main() {
    Outer: for {
        try {} finally {
            if true {
                { break Outer }
            }
        }
    }
}`)
	err := compiler.CompileFilesWithOptions([]string{source}, filepath.Join(root, "output"),
		compiler.CompileOptions{ModulePath: "example.com/exception-branches-diagnostic"})
	if err == nil || !strings.Contains(err.Error(), "control transfer from finally is not allowed: break") {
		t.Fatalf("expected finally transfer diagnostic, got %v", err)
	}
}
