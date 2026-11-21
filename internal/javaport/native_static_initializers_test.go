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

func TestNativeGPPStaticInitializersMatchJava(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	reference := filepath.Join(root, "Probe.java")
	writeTestFile(t, reference, `class Probe {
    static String events = "";
    static int record(int digit) { events += digit; return digit; }
    static int reject() { record(9); throw new IllegalStateException("rejected"); }

    static class Parent {
        static int Count = record(1);
        static { Count += record(2); }
        static int Read() { return Count; }
    }
    static class Child extends Parent {
        static int Own = Count + record(3);
        Child(int argument) { record(argument); }
    }
    static class Setter {
        static int Target = record(6);
    }
    static class A {
        static int Value = B.Value + 1;
    }
    static class B {
        static int Value = A.Value + 1;
    }
    static class Bad {
        static int Value = reject();
    }
    static class BadChild extends Bad {}
    static class Direct {
        static { record(7); }
        static void Use() {}
    }
    static class Numbers {
        static int Value = 3;
        int Read() { return Value; }
    }
    static class Lookup extends Numbers {
        static int Value = 10;
        static int Zero = Lookup.Later;
        static { Later = 7; }
        static int Later;
        static int Scope(int Value) {
            int before = Later;
            { int Later = 40; before += Later; }
            java.util.function.IntUnaryOperator local = Later -> Later + Value;
            return before + Later + local.applyAsInt(2) + Lookup.Value;
        }
        static int LocalClassName() {
            int Lookup = 11;
            return Lookup + Value;
        }
        int Instance = Value;
    }
    static class ParameterBase {
        int Number;
        ParameterBase(int argument) { Number = argument; }
        ParameterBase(String argument) { Number = argument.length(); }
    }
    static class ParameterChild extends ParameterBase {
        static String Argument = "seven";
        ParameterChild() { super(Argument); }
    }
    static String failure(Runnable call) {
        try { call.run(); return ""; }
        catch (LinkageError e) { return e.getClass().getName(); }
    }
    public static void main(String[] args) {
        System.out.println(Child.class.getSimpleName() + ":" + events);
        System.out.println(Child.Count + " " + Child.Read() + " " + events);
        new Child(record(4));
        System.out.println(Child.Own + " " + events);
        Setter.Target = record(5);
        System.out.println(Setter.Target + " " + events);
        System.out.println(A.Value + " " + B.Value);
        Direct.Use();
        var lookup = new Lookup();
        System.out.println(Lookup.Zero + " " + Lookup.Scope(6) + " " + Lookup.LocalClassName() + " "
                + lookup.Read() + " " + lookup.Instance + " " + new ParameterChild().Number);
        System.out.println(failure(() -> new BadChild()) + " " + events);
        System.out.println(failure(() -> new BadChild()) + " " + failure(() -> { int v = Bad.Value; }) + " " + events);
    }
}`)
	classes := filepath.Join(root, "classes")
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"),
		"--release", strconv.Itoa(release), "-d", classes, reference)
	want := runObjectTestCommand(t, root, java, "-cp", classes, "Probe")

	trace := filepath.Join(root, "trace.gpp")
	writeTestFile(t, trace, `package reference.statics.trace

import "errors"
import "strconv"

var Events string
var Rejected = errors.New("rejected")

func Record(digit int) int {
    Events += strconv.Itoa(digit)
    return digit
}

func Reject() int {
    Record(9)
    throw Rejected
}
`)
	parent := filepath.Join(root, "parent.gpp")
	writeTestFile(t, parent, `package reference.statics.parent

import reference.statics.trace

class Parent {
    static Count int = trace.Record(1)
    static { Count += trace.Record(2) }
    static func Read() int { return Count }
}
`)
	children := filepath.Join(root, "children.gpp")
	writeTestFile(t, children, `package reference.statics.children

import reference.statics.parent
import reference.statics.trace

class Child : parent.Parent {
    static Own int = Count + trace.Record(3)
    func init(argument int) { trace.Record(argument) }
}
class Setter {
    static Target int = trace.Record(6)
}
class A {
    static Value int = B.Value + 1
}
class B {
    static Value int = A.Value + 1
}
class Bad {
    static Value int = trace.Reject()
}
class BadChild : Bad {}
class Direct {
    static { trace.Record(7) }
    static func Use() {}
}

class Numbers {
    static Value int = 3
    func Read() int { return Value }
}

class Lookup : Numbers {
    static Value int = 10
    static Zero int = Lookup.Later
    static { Later = 7 }
    static Later int

    static func Scope(Value int) int {
        var before = Later
        { Later := 40; before += Later }
        local := func(Later int) int { return Later + Value }
        return before + Later + local(2) + Lookup.Value
    }

    static func LocalClassName() int {
        Lookup := 11
        return Lookup + Value
    }

    Instance int = Value
    static func Interpolated() string { return "value={{ Value }}" }
}

class ParameterBase {
    Number int
    func init(argument int) { this.Number = argument }
    func init(argument string) { this.Number = len(argument) }
}

class ParameterChild : ParameterBase {
    static Argument string = "seven"
    func init() { super(Argument) }
}

func ConstructorProbe() int {
    child, err := ParameterChild()
    if err != nil { panic(err) }
    return child.Number
}
func LookupInstance() *Lookup { return Lookup() }

func ChildName() string { return Child.class.Name }
func Base() (int, int) { return Child.Count, Child.Read() }
func Create() *Child { return Child(trace.Record(4)) }
func Set() { Setter.Target = trace.Record(5) }
func Cycle() (int, int) { return A.Value, B.Value }
func FailedChild() { _ = BadChild() }
func FailedParent() { _ = Bad.Value }

func Shadow() int {
    type other struct { Value int }
    A := other{Value: 87}
    return A.Value
}
`)
	output := filepath.Join(root, "generated")
	inputs := []string{trace, parent, children}
	options := compiler.CompileOptions{ModulePath: "example.com/static-initializers", IncrementalOutput: true}
	if err := compiler.CompileFilesWithOptions(inputs, output, options); err != nil {
		t.Fatal(err)
	}
	// The initial output cleanup runs before the manifest exists. A
	// subsequent incremental build persists its parse cache.
	if err := compiler.CompileFilesWithOptions(inputs, output, options); err != nil {
		t.Fatal(err)
	}
	cacheEntries, err := filepath.Glob(filepath.Join(output, ".gpp-parse-cache", "*.gob"))
	if err != nil || len(cacheEntries) < len(inputs) {
		t.Fatalf("expected cached syntax for each source, got %d files: %v", len(cacheEntries), err)
	}
	// Force generation from restored class ASTs by changing an imported file.
	traceSource, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, trace, string(traceSource)+"\n// Rebuild dependants.\n")
	if err := compiler.CompileFilesWithOptions(inputs, output, options); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(output, "main.go"), `package main

import (
    "errors"
    "fmt"

    "example.com/static-initializers/reference/statics/children"
    "example.com/static-initializers/reference/statics/parent"
    "example.com/static-initializers/reference/statics/trace"
)

func failure(call func()) (name string) {
    defer func() {
        if recovered := recover(); recovered != nil {
            failure := recovered.(interface{ GppThrownError() error }).GppThrownError()
            if !errors.Is(failure, trace.Rejected) { panic("original failure was lost") }
            name = failure.(interface{ JavaClassName() string }).JavaClassName()
        }
    }()
    call()
    return
}

func main() {
    _ = children.Child{}
    if children.Shadow() != 87 { panic("class name shadowing was lost") }
    fmt.Println(children.ChildName() + ":" + trace.Events)
    first, second := children.Base()
    if parent.GppStatic_Parent_Read() != second { panic("direct Go call returned a different value") }
    fmt.Println(first, second, trace.Events)
    children.Create()
    fmt.Println(children.GppGetStatic_Child_Own(), trace.Events)
    children.Set()
    fmt.Println(children.GppGetStatic_Setter_Target(), trace.Events)
    fmt.Println(children.Cycle())
    children.GppStatic_Direct_Use()
    lookup := children.LookupInstance()
    fmt.Println(children.GppGetStatic_Lookup_Zero(), children.GppStatic_Lookup_Scope(6),
        children.GppStatic_Lookup_LocalClassName(), lookup.Read(), lookup.Instance, children.ConstructorProbe())
    if got := children.GppStatic_Lookup_Interpolated(); got != "value=10" { panic(got) }
    fmt.Println(failure(children.FailedChild), trace.Events)
    fmt.Println(failure(children.FailedChild), failure(children.FailedParent), trace.Events)
}
`)
	got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", ".")
	if got != want {
		t.Fatalf("GPP and Java static initialization diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}

func TestNativeGPPStaticInitializerPublishesOnce(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "main.gpp")
	writeTestFile(t, source, `package main

import "sync/atomic"

var attempts atomic.Int32
var entered = make(chan struct{})
var release = make(chan struct{})

class Shared {
    static Value int = initialize()
    static func Read() int { return Shared.Value }
}

class Broken {
    static { panic("native Go panic") }
    static func Use() {}
}

func initialize() int {
    attempts.Add(1)
    close(entered)
    <-release
    return 42
}
`)
	output := filepath.Join(root, "generated")
	if err := compiler.CompileFiles([]string{source}, output); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(output, "verify_test.go"), `package main

import "testing"

func TestConcurrentReads(t *testing.T) {
    const readers = 12
    values := make(chan int, readers)
    go func() { values <- GppStatic_Shared_Read() }()
    <-entered
    started := make(chan struct{}, readers-1)
    for range readers-1 {
        go func() {
            started <- struct{}{}
            values <- GppGetStatic_Shared_Value()
        }()
    }
    for range readers-1 { <-started }
    select {
    case value := <-values:
        t.Fatalf("reader returned while class was initializing: %d", value)
    default:
    }
    close(release)
    for range readers {
        if value := <-values; value != 42 {
            t.Fatalf("read before initialization: %d", value)
        }
    }
    if value := attempts.Load(); value != 1 {
        t.Fatalf("initializer ran %d times", value)
    }
}
func TestNativePanicPreserved(t *testing.T) {
    for range 2 {
        func() {
            defer func() {
                if value := recover(); value != "native Go panic" {
                    t.Fatalf("Go panic changed: %v", value)
                }
            }()
            GppStatic_Broken_Use()
        }()
    }
}
`)
	// Command-line files exercise the standalone runtime without a module.
	runObjectTestCommand(t, output, "go", "test", "-timeout=15s", "main.go", "verify_test.go")
}

func TestNativeGPPStaticForwardReferenceDiagnosticsMatchJava(t *testing.T) {
	java, release := referenceJDK(t)
	tests := []struct {
		name string
		java string
		gpp  string
		line int
	}{
		{"later field", "static int First = Later;\nstatic int Later = 1;",
			"static First int = Later\nstatic Later int = 1", 3},
		{"self reference", "static int Later = Later;",
			"static Later int = Later", 3},
		{"nested shadowing", "static { { int Later = 0; } int First = Later; }\nstatic int Later;",
			"static { { Later := 0; _ = Later }; _ = Later }\nstatic Later int", 3},
		{"compound assignment", "static { Later += 1; }\nstatic int Later;",
			"static { Later += 1 }\nstatic Later int", 3},
		{"increment", "static { Later++; }\nstatic int Later;",
			"static { Later++ }\nstatic Later int", 3},
		{"lambda body", "static Runnable Run = () -> System.out.println(Later);\nstatic int Later;",
			"static Run func() = func() { println(Later) }\nstatic Later int", 3},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			reference := filepath.Join(root, "Probe.java")
			writeTestFile(t, reference, "class Probe {\n"+test.java+"\n}")
			process := exec.CommandContext(t.Context(), filepath.Join(filepath.Dir(java), "javac"),
				"--release", strconv.Itoa(release), "-d", root, reference)
			output, err := process.CombinedOutput()
			want := "illegal forward reference"
			if test.name == "self reference" {
				want = "self-reference in initializer"
			}
			if err == nil || !strings.Contains(string(output), want) {
				t.Fatalf("expected javac to reject the forward reference: %v\n%s", err, output)
			}

			source := "package main\nclass Probe {\n" + test.gpp + "\n}"
			file, err := compiler.ParseFile("probe.gpp", source)
			if err != nil {
				t.Fatal(err)
			}
			_, err = compiler.Emit(file)
			if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "static field Later") ||
				!strings.Contains(err.Error(), "probe.gpp:"+strconv.Itoa(test.line)+":") {
				t.Fatalf("expected a forward reference diagnostic at the source use, got %v", err)
			}
		})
	}
}
