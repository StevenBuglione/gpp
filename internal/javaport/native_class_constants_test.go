package javaport

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestNativeGPPClassConstantsMatchJava(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	reference := filepath.Join(root, "Probe.java")
	writeTestFile(t, reference, `class Probe {
    static String Trace = "";
    static int mark(String text) { Trace += text; return Trace.length(); }
    static class Base {
        static int Runtime = mark("base");
        static final int Count = 6;
        static final int Mask = (Count << 1) | 1;
        static final String Key = "spring" + ".port";
        static final int Forward = Base.Later;
        static final int Later = 8;
        static final boolean Enabled = Mask == 13 && Forward == 8;
    }
    static class Leaf extends Base {
        static { mark("/leaf"); }
        static final int Count = Base.Count + 1;
        static final String Key = Base.Key + ".routed";
        static int Active() { return Count; }
    }
    static class Invalid {
        static final int Value = 19;
        static int Broken = fail();
        static int fail() { throw new IllegalStateException("broken"); }
    }
    public static void main(String[] args) {
        final int bound = Leaf.Mask - Leaf.Count;
        System.out.println(bound + " " + Leaf.Key + " " + Leaf.Enabled + " " + Leaf.Forward + " " + Invalid.Value);
        System.out.println("trace=" + Trace);
        System.out.println(Leaf.Runtime + " " + Trace);
        System.out.println(Leaf.Active() + " " + Trace);
        try { int unused = Invalid.Broken; }
        catch (ExceptionInInitializerError expected) { System.out.println(Invalid.Value); }
    }
}`)
	classes := filepath.Join(root, "classes")
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"),
		"--release", strconv.Itoa(release), "-d", classes, reference)
	want := runObjectTestCommand(t, root, java, "-cp", classes, "Probe")

	base := filepath.Join(root, "base.gpp")
	writeTestFile(t, base, `package reference.constants.base

var Trace string

func Mark(text string) int {
    Trace += text
    return len(Trace)
}

class Base {
    static Runtime int = Mark("base")
    static const Count int = 6
    static const Mask int = (Count << 1) | 1
    static const Key string = "spring" + ".port"
    static const Forward int = Base.Later
    static const Later int = 8
    static const Enabled bool = Mask == 13 && Forward == 8
}
`)
	leaf := filepath.Join(root, "leaf.gpp")
	writeTestFile(t, leaf, `package reference.constants.leaf

import reference.constants.base

class Leaf : base.Base {
    static { base.Mark("/leaf") }
    static const Count int = base.Base.Count + 1
    static const Key string = base.Base.Key + ".routed"
    static func Active() int { return Count }
}

class Invalid {
    static const Value int = 19
    static Broken int = Invalid.Fail()
    static func Fail() int { throw brokenError("broken") }
}

type brokenError string
func (failure brokenError) Error() string { return string(failure) }
`)
	probe := filepath.Join(root, "probe.gpp")
	writeTestFile(t, probe, `package reference.constants.probe

import reference.constants.leaf

const Bound = leaf.Leaf.Mask - leaf.Leaf.Count
const Key = leaf.Leaf.Key
const Enabled = leaf.Leaf.Enabled
const Forward = leaf.Leaf.Forward

type Slots [leaf.Leaf.Mask - leaf.Leaf.Count]byte

func ReadRuntime() int { return leaf.Leaf.Runtime }
func Fail() { _ = leaf.Invalid.Broken }
`)
	output := filepath.Join(root, "output")
	if err := compiler.CompileFilesWithOptions([]string{base, leaf, probe}, output,
		compiler.CompileOptions{ModulePath: "example.com/class-constants"}); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(output, "main.go"), `package main

import (
    "fmt"
    "example.com/class-constants/reference/constants/base"
    "example.com/class-constants/reference/constants/leaf"
    "example.com/class-constants/reference/constants/probe"
)

const GoMask = base.GppConst_Base_Mask
const GoInvalid = leaf.GppConst_Invalid_Value

func tryInvalid() {
    defer func() {
        recovered := recover()
        failure := recovered.(interface{ GppThrownError() error }).GppThrownError()
        if failure.(interface{ JavaClassName() string }).JavaClassName() != "java.lang.ExceptionInInitializerError" {
            panic(failure)
        }
        fmt.Println(GoInvalid)
    }()
    probe.Fail()
}

func main() {
    if GoMask != 13 || len(probe.Slots{}) != probe.Bound {
        panic("native Go constant or GPP array bound diverged")
    }
    fmt.Println(probe.Bound, probe.Key, probe.Enabled, probe.Forward, GoInvalid)
    fmt.Println("trace=" + base.Trace)
    fmt.Println(probe.ReadRuntime(), base.Trace)
    fmt.Println(leaf.GppStatic_Leaf_Active(), base.Trace)
    tryInvalid()
}
`)
	got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", ".")
	if got != want {
		t.Fatalf("GPP and Java constants diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}

func TestNativeGPPConstantOnlyClassesNeedNoInitializationRuntime(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "probe.gpp")
	writeTestFile(t, source, `package main

import "math"

class Values {
    static const Limit int = math.MaxInt8
    static const Count int = Limit / 2
    func Counted() int { return Count }
}

func main() {
    if Values().Counted() != 63 { panic("constant-only class returned the wrong value") }
}
`)
	output := filepath.Join(root, "output")
	if err := compiler.CompileFilesWithOptions([]string{source}, output,
		compiler.CompileOptions{ModulePath: "example.com/constant-only"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(output, "gpp", "classinit", "runtime.go")); !os.IsNotExist(err) {
		t.Fatalf("constant-only class emitted class initialization runtime: %v", err)
	}
	runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", ".")
}

func TestNativeGPPClassConstantsRejectMutationAndCycles(t *testing.T) {
	tests := []struct{ name, declarations, diagnostic string }{
		{"assign", "static const Limit int = 10\nstatic func Set() { Limit = 3 }", "constant"},
		{"increment", "static const Limit int = 10\nstatic func Set() { Probe.Limit++ }", "constant"},
		{"address", "static const Limit int = 10\nstatic func Get() *int { return &Limit }", "constant"},
		{"cycle", "static const Limit int = Probe.Other\nstatic const Other int = Limit", "initialization cycle"},
		{"computed at runtime", "static const Limit int = read()", "not constant"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "probe.gpp")
			writeTestFile(t, source, "package main\nclass Probe {\n"+test.declarations+"\n}\nfunc main() {}\nfunc read() int { return 1 }")
			output := filepath.Join(root, "output")
			if err := compiler.CompileFilesWithOptions([]string{source}, output,
				compiler.CompileOptions{ModulePath: "example.com/invalid-class-constant"}); err != nil {
				t.Fatal(err)
			}
			build := exec.CommandContext(t.Context(), "go", "build", "-buildvcs=false", ".")
			build.Dir = output
			result, err := build.CombinedOutput()
			if err == nil || !strings.Contains(string(result), test.diagnostic) {
				t.Fatalf("expected Go's %q diagnostic: %v\n%s", test.diagnostic, err, result)
			}
		})
	}
}
