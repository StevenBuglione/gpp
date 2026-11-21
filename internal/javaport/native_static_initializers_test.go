package javaport

import (
	"path/filepath"
	"strconv"
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
        static { Parent.Count += record(2); }
        static int Read() { return Parent.Count; }
    }
    static class Child extends Parent {
        static int Own = record(3);
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
    static { Parent.Count += trace.Record(2) }
    static func Read() int { return Parent.Count }
}
`)
	children := filepath.Join(root, "children.gpp")
	writeTestFile(t, children, `package reference.statics.children

import reference.statics.parent
import reference.statics.trace

class Child : parent.Parent {
    static Own int = trace.Record(3)
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
	if err := compiler.CompileFilesWithOptions([]string{trace, parent, children}, output,
		compiler.CompileOptions{ModulePath: "example.com/static-initializers"}); err != nil {
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
