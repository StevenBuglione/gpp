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
