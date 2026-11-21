package javaport

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestNativeFinallyCompletionMatchesJava(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	reference := filepath.Join(root, "Probe.java")
	writeTestFile(t, reference, `class Probe {
    static int trace;
    static final int assigned;
    static {
        Init: for (;;) {
            try {
                throw new IllegalArgumentException();
            } finally {
                break Init;
            }
        }
        assigned = 19;
    }

    static int stamp(int n) {
        trace = trace * 10 + n;
        return n;
    }

    static int tryReturn(boolean replace) {
        try {
            return stamp(1);
        } finally {
            stamp(2);
            if (replace) return stamp(3);
        }
    }

    static int catchReturn() {
        try {
            throw new IllegalArgumentException();
        } catch (IllegalArgumentException ignored) {
            return stamp(1);
        } finally {
            return stamp(2);
        }
    }

    static int throwReturn() {
        try {
            throw new IllegalArgumentException();
        } finally {
            return stamp(2);
        }
    }

    static int nestedReturns() {
        try {
            try {
                return stamp(1);
            } finally {
                return stamp(2);
            }
        } finally {
            return stamp(3);
        }
    }

    static int branches(int mode) {
        Outer: for (int i = 0; i < 2; i++) {
            Middle: for (int j = 0; j < 1; j++) {
                try {
                    try {
                        stamp(1);
                        if (mode == 0) return 9;
                        if (mode == 1) throw new IllegalArgumentException();
                        if (mode == 2) continue Outer;
                        break Outer;
                    } finally {
                        stamp(2);
                        if (mode == 1) continue Middle;
                        if (mode == 2) break Middle;
                        if (mode == 3) continue Outer;
                        break Outer;
                    }
                } finally {
                    stamp(3);
                }
            }
            stamp(4);
        }
        return stamp(5);
    }

    static int nestedBranch() {
        for (int i = 0; i < 1; i++) {
            try {
                return stamp(1);
            } finally {
                try {
                    break;
                } finally {
                    stamp(2);
                }
            }
        }
        return stamp(3);
    }

    static int branchReturn(boolean replace) {
        Loop: for (;;) {
            try {
                stamp(1);
                break Loop;
            } finally {
                if (replace) return stamp(2);
            }
        }
        return stamp(3);
    }

    static long wideResult() {
        try {
            return 1;
        } finally {
            return 2;
        }
    }

    static Object nullResult() {
        try {
            return new Object();
        } finally {
            return null;
        }
    }

    static void report(int value) {
        System.out.println(value + " " + trace);
        trace = 0;
    }

    public static void main(String[] args) {
        System.out.println(assigned);
        report(tryReturn(false));
        report(tryReturn(true));
        report(catchReturn());
        report(throwReturn());
        report(nestedReturns());
        for (int mode = 0; mode < 4; mode++) report(branches(mode));
        report(nestedBranch());
        report(branchReturn(false));
        report(branchReturn(true));
        System.out.println(wideResult() + " " + (nullResult() == null));
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
    static final Assigned int
    static {
        Init: for {
            try {
                throw errors.New("initialization")
            } finally {
                break Init
            }
        }
        Assigned = 19
    }
}

var trace int
func stamp(n int) int {
    trace = trace * 10 + n
    return n
}

func tryReturn(replace bool) int {
    try {
        return stamp(1)
    } finally {
        stamp(2)
        if replace { return stamp(3) }
    }
}

func catchReturn() int {
    try {
        throw errors.New("try")
    } catch error {
        return stamp(1)
    } finally {
        return stamp(2)
    }
}

func throwReturn() int {
    try {
        throw errors.New("try")
    } finally {
        return stamp(2)
    }
}

func nestedReturns() int {
    try {
        try {
            return stamp(1)
        } finally {
            return stamp(2)
        }
    } finally {
        return stamp(3)
    }
}

func branches(mode int) int {
    Outer: for i := 0; i < 2; i++ {
        Middle: for j := 0; j < 1; j++ {
            try {
                try {
                    stamp(1)
                    if mode == 0 { return 9 }
                    if mode == 1 { throw errors.New("try") }
                    if mode == 2 { continue Outer }
                    break Outer
                } finally {
                    stamp(2)
                    if mode == 1 { continue Middle }
                    if mode == 2 { break Middle }
                    if mode == 3 { continue Outer }
                    break Outer
                }
            } finally {
                stamp(3)
            }
        }
        stamp(4)
    }
    return stamp(5)
}

func nestedBranch() int {
    for i := 0; i < 1; i++ {
        try {
            return stamp(1)
        } finally {
            try {
                break
            } finally {
                stamp(2)
            }
        }
    }
    return stamp(3)
}

func branchReturn(replace bool) int {
    Loop: for {
        try {
            stamp(1)
            break Loop
        } finally {
            if replace { return stamp(2) }
        }
    }
    return stamp(3)
}

func wideResult() int64 {
    try {
        return 1
    } finally {
        return 2
    }
}

func nullResult() any {
    try {
        return new(int)
    } finally {
        return nil
    }
}

func report(value int) {
    fmt.Println(value, trace)
    trace = 0
}
func main() {
    fmt.Println(Probe.Assigned)
    report(tryReturn(false))
    report(tryReturn(true))
    report(catchReturn())
    report(throwReturn())
    report(nestedReturns())
    for mode := 0; mode < 4; mode++ { report(branches(mode)) }
    report(nestedBranch())
    report(branchReturn(false))
    report(branchReturn(true))
    fmt.Println(wideResult(), nullResult() == nil)
}`)
	output := filepath.Join(root, "output")
	if err := compiler.CompileFilesWithOptions([]string{source}, output,
		compiler.CompileOptions{ModulePath: "example.com/finally-completion"}); err != nil {
		t.Fatal(err)
	}
	if got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", "."); got != want {
		t.Fatalf("GPP and Java finally completion diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}

func TestNativeFinallyCompletionWithGoPanicAndLocalBranches(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "main.gpp")
	writeTestFile(t, source, `package main
import "fmt"

func rawNormal() {
    try {
        panic("raw")
    } catch error {
        fmt.Println("unexpected catch")
    } finally {
        fmt.Println("normal cleanup")
    }
}

func rawBranch() {
    for {
        try {
            panic("replaced by break")
        } finally {
            recover := "branch cleanup"
            fmt.Println(recover)
            break
        }
    }
}

func rawReturn() int {
    try {
        panic("replaced by return")
    } finally {
        return 7
    }
}

func rawUserRecover() {
    for {
        try {
            panic("user recovered")
        } finally {
            fmt.Println(recover())
            break
        }
    }
}

func nativePair() (int64, error) { return 14, nil }

func returnPair() (int64, error) {
    try {
        return 7, nil
    } finally {
        return nativePair()
    }
}

func returnNamed() (left int64, right int64) {
    try {
        return 1, 2
    } finally {
        return 3, 4
    }
}

func returnShadowed() int64 {
    try {
        return 1
    } finally {
        int64 := "shadowed type name"
        fmt.Println(int64)
        return 6
    }
}

func localBranches() {
    try {} finally {
        Local: for i := 0; i < 2; i++ {
            if i == 0 { continue Local }
            break Local
        }
        switch 1 {
            case 1:
                fmt.Println("first case")
                fallthrough
            case 2:
                fmt.Println("second case")
                break
        }
    }
}

func main() {
    func() {
        defer func() { fmt.Println(recover()) }()
        rawNormal()
    }()
    rawBranch()
    fmt.Println(rawReturn())
    rawUserRecover()
    value, err := returnPair()
    fmt.Println(value, err == nil)
    left, right := returnNamed()
    fmt.Println(left, right)
    fmt.Println(returnShadowed())
    localBranches()
}`)
	output := filepath.Join(root, "output")
	if err := compiler.CompileFilesWithOptions([]string{source}, output,
		compiler.CompileOptions{ModulePath: "example.com/finally-go"}); err != nil {
		t.Fatal(err)
	}
	want := "normal cleanup\nraw\nbranch cleanup\n7\nuser recovered\n14 true\n3 4\nshadowed type name\n6\nfirst case\nsecond case"
	if got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", "."); strings.TrimSpace(got) != want {
		t.Fatalf("unexpected Go interop trace %q", got)
	}
}
