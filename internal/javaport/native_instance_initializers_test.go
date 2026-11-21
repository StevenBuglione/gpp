package javaport

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestNativeGPPInstanceInitializersMatchJava(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	reference := filepath.Join(root, "Probe.java")
	writeTestFile(t, reference, `class Probe {
    static int events;
    static final RuntimeException REJECTED = new RuntimeException("initializer rejected");
    static int record(int digit) { events = events * 10 + digit; return digit; }
    static int reject() { record(4); throw REJECTED; }

    static class Parent {
        int seed = record(1);
        { seed += record(2); }
        int observed;
        Parent() { record(3); observed = read(); }
        protected int read() { return 100; }
    }
    static class Middle extends Parent { int middle = record(4); }
    static class Child extends Middle {
        int beforeLater = this.later;
        int payload = record(5);
        { int plus = record(6); payload += plus; }
        int later = 90;
        String text = "count=" + payload;
        Child() { this(0); record(8); }
        Child(int ignored) { super(); record(7); }
        @Override protected int read() { return payload; }
    }
    static class DefaultChild extends Middle { int own = record(5); }
    static class Unhappy extends Parent {
        int field = reject();
        { record(5); }
        Unhappy() { super(); record(6); }
    }
    public static void main(String[] args) {
        var child = new Child();
        System.out.println(events + " " + child.seed + " " + child.observed + " " + child.middle +
                " " + child.beforeLater + " " + child.payload + " " + child.later + " " + child.text);
        events = 0;
        var defaultChild = new DefaultChild();
        System.out.println(events + " " + defaultChild.own);
        events = 0;
        Unhappy value = null;
        try { value = new Unhappy(); }
        catch (RuntimeException failure) {
            System.out.println(events + " " + (failure == REJECTED) + " " + (value == null));
        }
    }
}`)
	classes := filepath.Join(root, "classes")
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"),
		"--release", strconv.Itoa(release), "-d", classes, reference)
	want := runObjectTestCommand(t, root, java, "-cp", classes, "Probe")
	trace := filepath.Join(root, "trace.gpp")
	writeTestFile(t, trace, `package reference.instance.trace

import "errors"

var Events int32
var Rejected = errors.New("initializer rejected")

func Record(digit int32) int32 {
    Events = Events * 10 + digit
    return digit
}
func Reject() (int32, error) {
    _ = Record(4)
    return 0, Rejected
}
`)
	parent := filepath.Join(root, "parent.gpp")
	writeTestFile(t, parent, `package reference.instance.parent

import reference.instance.trace

class Parent {
    Seed int32 = trace.Record(
        1,
    )
    {
        Seed += trace.Record(2)
    }
    Observed int32
    func init() {
        trace.Record(3)
        this.Observed = this._read()
    }
    func _read() int32 { return 100 }
}
`)
	middle := filepath.Join(root, "middle.gpp")
	writeTestFile(t, middle, `package reference.instance.middle

import reference.instance.parent
import reference.instance.trace

class Middle : parent.Parent {
    MiddleValue int32 =
        trace.Record(4)
}
`)
	children := filepath.Join(root, "children.gpp")
	writeTestFile(t, children, `package reference.instance.children

import reference.instance.parent
import reference.instance.middle
import reference.instance.trace

annotation Label(value string) on field

class Child : middle.Middle {
    BeforeLater int32 = this.Later
    Payload int32 = trace.Record(
        5,
    ) @{Label("payload")}
    {
        plus := trace.Record(6)
        this.Payload += plus
    }
    Later int32 = 90
    Text string = "count={{ this.Payload }}"
    func init() {
        this(int32(0))
        trace.Record(8)
    }
    func init(ignored int32) {
        super()
        trace.Record(7)
    }
    func _read() int32 { return this.Payload }
}

class DefaultChild : middle.Middle {
    Own int32 = trace.Record(5)
}

class Unhappy : parent.Parent {
    Failed int32 = trace.Reject()
    { trace.Record(5) }
    func init() error {
        super()
        trace.Record(6)
        return nil
    }
}

func NewChild() *Child { return Child() }
func NewDefaultChild() *DefaultChild { return DefaultChild() }
func NewNamedDefaultChild() *DefaultChild { return DefaultChild(Own: 99) }
func NewUnhappy() (*Unhappy, error) { return Unhappy() }
`)
	output := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions([]string{trace, parent, middle, children}, output,
		compiler.CompileOptions{ModulePath: "example.com/instance-initializers"}); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(output, "main.go"), `package main

import (
    "fmt"
    "example.com/instance-initializers/reference/instance/children"
    "example.com/instance-initializers/reference/instance/trace"
)

func main() {
    child := children.NewChild()
    fmt.Println(trace.Events, child.Seed, child.Observed, child.MiddleValue, child.BeforeLater, child.Payload, child.Later, child.Text)
    trace.Events = 0
    defaultChild := children.NewDefaultChild()
    fmt.Println(trace.Events, defaultChild.Own)
    trace.Events = 0
    value, err := children.NewUnhappy()
    fmt.Println(trace.Events, err == trace.Rejected, value == nil)

    trace.Events = 0
    raw := children.Child{}
    if trace.Events != 0 || raw.Payload != 0 { panic("Go literal ran an initializer") }
    named := children.NewNamedDefaultChild()
    if named.Own != 5 || trace.Events != 12345 { panic("field-based construction used a different initialization order") }
    for _, field := range children.GppChildClass.Fields {
        if field.Name == "Payload" {
            if !field.Annotations.Has(children.GppAnnotation_Label) { panic("field annotation lost during initialization parsing") }
            return
        }
    }
    panic("initialized field metadata missing")
}
`)
	got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", ".")
	if got != want {
		t.Fatalf("GPP and Java instance initialization diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}
