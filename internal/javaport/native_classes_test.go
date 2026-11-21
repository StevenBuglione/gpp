package javaport

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestNativeGPPConstructorDispatchMatchesJava(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	reference := filepath.Join(root, "Probe.java")
	writeTestFile(t, reference, `class Probe {
    static class Base {
        private final int during;
        Base() { during = observe(); }
        private int observe() { return value(); }
        protected int value() { return 100; }
        public int during() { return during; }
        public int read() { return value(); }
    }
    static class Middle extends Base {
        Middle() { super(); }
        @Override protected int value() { return 50; }
    }
    static class Child extends Middle {
        private int setting;
        Child(int setting) { super(); this.setting = setting; }
        @Override protected int value() { return setting; }
    }
    static class DefaultChild extends Base {
        @Override protected int value() { return 7; }
    }
    public static void main(String[] args) {
        Base plain = new Base();
        Base child = new Child(41);
        Base defaultChild = new DefaultChild();
        Base middle = new Middle();
        System.out.println(plain.during() + " " + plain.read());
        System.out.println(child.during() + " " + child.read());
        System.out.println(defaultChild.during() + " " + defaultChild.read());
        System.out.println(middle.during() + " " + middle.read());
    }
}`)
	classes := filepath.Join(root, "classes")
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"),
		"--release", strconv.Itoa(release), "-d", classes, reference)
	want := runObjectTestCommand(t, root, java, "-cp", classes, "Probe")
	base := filepath.Join(root, "base.gpp")
	writeTestFile(t, base, `package reference.native.base

class Base {
    during int32

    func init() { this.during = this.observe() }
    func observe() int32 {
        value := this._value
        return value()
    }
    func _value() int32 { return 100 }
    func During() int32 { return this.during }
    func Read() int32 { return this._value() }
}

func NewBase() *Base { return Base() }
`)
	children := filepath.Join(root, "children.gpp")
	writeTestFile(t, children, `package reference.native.children

import reference.native.base

class Middle : base.Base {
    func init() { super() }
    func _value() int32 { return 50 }
}

class Child : Middle {
    setting int32
    func init(setting int32) {
        super()
        this.setting = setting
    }
    func _value() int32 { return this.setting }
    func Set(value int32) { this.setting = value }
}

class DefaultChild : base.Base {
    func _value() int32 { return 7 }
}

func NewChild(setting int32) *Child { return Child(setting) }
func NewDefaultChild() *DefaultChild { return DefaultChild() }
func NewMiddle() *Middle { return Middle() }
`)
	output := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions([]string{base, children}, output,
		compiler.CompileOptions{ModulePath: "example.com/native-classes"}); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(output, "main.go"), `package main

import (
    "fmt"
    "example.com/native-classes/reference/native/base"
    "example.com/native-classes/reference/native/children"
)

func main() {
    plain := base.NewBase()
    child := children.NewChild(41)
    defaultChild := children.NewDefaultChild()
    middle := children.NewMiddle()
    for _, object := range []*base.Base{plain, &child.Middle.Base, &defaultChild.Base, &middle.Base} {
        fmt.Println(object.During(), object.Read())
    }
    var goZero base.Base
    if goZero.Read() != 100 { panic("plain Go zero value lost its base behavior") }
    copied := *child
    copied.Set(23)
    if copied.Read() != 23 || child.Read() != 41 { panic("Go copy dispatched to the original instance") }
}
`)
	got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", ".")
	if got != want {
		t.Fatalf("native GPP and Java constructor dispatch diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}

func TestNativeGPPFlexibleConstructorsMatchJava(t *testing.T) {
	java, release := referenceJDK(t)
	if release < 25 {
		t.Skip("flexible constructor bodies are finalized in Java SE 25")
	}
	root := t.TempDir()
	reference := filepath.Join(root, "Probe.java")
	writeTestFile(t, reference, `class Probe {
    static final RuntimeException EARLY = new RuntimeException("early");
    static final RuntimeException PARENT = new RuntimeException("parent");
    static class Trace {
        int digits;
        void mark(int digit) { digits = digits * 10 + digit; }
    }
    static class Parent {
        int observed;
        int input;
        Parent(Trace t, int input) {
            t.mark(3);
            if (input == 26) { throw PARENT; }
            this.observed = read();
            this.input = input;
        }
        Parent(Trace t, String input) { t.mark(9); }
        int read() { return 100; }
    }
    static class Child extends Parent {
        int early;
        Child(Trace t) {
            t.mark(1);
            int setting = 8;
            this(t, setting);
            t.mark(5);
        }
        Child(Trace t, int setting) {
            t.mark(2);
            if (setting < 0) { throw EARLY; }
            if (setting > 0) { this.early = setting; }
            int forwarded = setting * 2;
            super(t, forwarded);
            t.mark(4);
        }
        @Override int read() { return early; }
    }
    static class VoidChild extends Parent {
        VoidChild(Trace t, int setting) {
            t.mark(2);
            int forwarded = setting * 2;
            super(t, forwarded);
            t.mark(4);
        }
    }
    static class Marker {}
    static class DefaultParent extends Marker {}
    static class DefaultCall extends DefaultParent {
        DefaultCall(Trace t) {
            t.mark(2);
            super();
            t.mark(4);
        }
    }
    public static void main(String[] args) {
        var t = new Trace();
        var value = new Child(t);
        System.out.println(t.digits + " " + value.observed + " " + value.input);
        for (int input : new int[]{-1, 13}) {
            var failed = new Trace();
            try { new Child(failed, input); }
            catch (RuntimeException e) { System.out.println(failed.digits + " " + (e == EARLY || e == PARENT) + " " + e.getMessage()); }
        }
        var failed = new Trace();
        try { new VoidChild(failed, 13); }
        catch (RuntimeException e) { System.out.println(failed.digits + " " + (e == PARENT) + " " + e.getMessage()); }
        var defaultTrace = new Trace();
        new DefaultCall(defaultTrace);
        System.out.println(defaultTrace.digits);
    }
}`)
	classes := filepath.Join(root, "classes")
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"),
		"--release", strconv.Itoa(release), "-d", classes, reference)
	want := runObjectTestCommand(t, root, java, "-cp", classes, "Probe")
	source := filepath.Join(root, "constructors.gpp")
	writeTestFile(t, source, `package reference.flexible

import "errors"

var EarlyFailure = errors.New("early")
var ParentFailure = errors.New("parent")

class Trace {
    Digits int32
    func Mark(digit int32) { this.Digits = this.Digits * 10 + digit }
}

class Parent {
    Observed int32
    Input int32
    func init(t *Trace, input int32) error {
        t.Mark(3)
        if input == 26 { return ParentFailure }
        this.Observed = this._read()
        this.Input = input
        return nil
    }
    func init(t *Trace, input string) { t.Mark(9) }
    func _read() int32 { return 100 }
}

class Child : Parent {
    early int32
    func init(t *Trace) {
        t.Mark(1)
        var setting int32 = 8
        this(t, setting)
        t.Mark(5)
    }
    func init(t *Trace, setting int32) error {
        t.Mark(2)
        if setting < 0 { throw EarlyFailure }
        if setting > 0 { this.early = setting }
        var forwarded int32 = setting * 2
        super(t, forwarded)
        t.Mark(4)
        return nil
    }
    func _read() int32 { return this.early }
}

class VoidChild : Parent {
    func init(t *Trace, setting int32) {
        t.Mark(2)
        var forwarded int32 = setting * 2
        if setting == 13 {
            var forwarded string = "inner scope"
            _ = forwarded
        }
        super(input: forwarded, t: t)
        t.Mark(4)
    }
}

class Marker {}
class DefaultParent : Marker {}
class DefaultCall : DefaultParent {
    func init(t *Trace) {
        t.Mark(2)
        super()
        t.Mark(4)
    }
}

func NewTrace() *Trace { return Trace() }
func NewChild(t *Trace) (*Child, error) { return Child(t) }
func NewChildWithSetting(t *Trace, input int32) (*Child, error) { return Child(t, input) }
func NewVoidChild(t *Trace, input int32) (*VoidChild, error) { return VoidChild(t, input) }
func NewDefaultCall(t *Trace) *DefaultCall { return DefaultCall(t) }
`)
	output := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions([]string{source}, output,
		compiler.CompileOptions{ModulePath: "example.com/flexible-constructors"}); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(output, "main.go"), `package main

import (
    "fmt"
    "example.com/flexible-constructors/reference/flexible"
)

func main() {
    trace := flexible.NewTrace()
    value, err := flexible.NewChild(trace)
    if err != nil { panic(err) }
    fmt.Println(trace.Digits, value.Observed, value.Input)
    for _, input := range []int32{-1, 13} {
        trace := flexible.NewTrace()
        instance, err := flexible.NewChildWithSetting(trace, input)
        if instance != nil || err == nil { panic("returned a partial instance") }
        fmt.Println(trace.Digits, err == flexible.EarlyFailure || err == flexible.ParentFailure, err)
    }
    trace = flexible.NewTrace()
    instance, err := flexible.NewVoidChild(trace, 13)
    if instance != nil || err == nil { panic("returned a partial instance") }
    fmt.Println(trace.Digits, err == flexible.ParentFailure, err)
    trace = flexible.NewTrace()
    _ = flexible.NewDefaultCall(trace)
    fmt.Println(trace.Digits)
}
`)
	got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", ".")
	if got != want {
		t.Fatalf("native GPP and Java flexible constructors diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}
