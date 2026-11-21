package javaport

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestNativeGPPStaticMethodScopeMatchesJava(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	reference := filepath.Join(root, "Probe.java")
	writeTestFile(t, reference, `class Probe {
    static class Root {}
    static class Middle extends Root {}
    static class Leaf extends Middle {}

    static class Parent {
        static int Pick(int v) { return v + 1; }
        static String Pick(String v) { return "parent:" + v; }
        static String Pick(String first, String last) { return first + "/" + last; }
        static <T> T Identity(T value) { return value; }
        static String Narrow(Root value) { return "root"; }
        static String Narrow(Middle value) { return "middle"; }
        static String Variadic(Root value) { return "fixed"; }
        static String Variadic(Leaf... value) { return "variadic:" + value.length; }
        static int Evaluate(int value, java.util.function.IntUnaryOperator callback) {
            return callback.applyAsInt(value);
        }
        int Lexical() { return Pick(10); }
    }
    static class Child extends Parent {
        static int Pick(int v) { return v + 20; }
        static String Snapshot = Pick("init");
        static String Pick(boolean v) { return v ? "on" : "off"; }
        static String Mix(int v) { return "static:" + v; }
        String Mix(String v) { return "instance:" + v; }
        static String Read() { return Identity(Snapshot + "/" + Pick(2) + "/" + Pick(true)); }
        String Mixed() { return Mix(3) + "/" + Mix("text"); }
        static String Narrowed() { return Narrow(new Leaf()) + "/" + Narrow(null); }
        static String Phase() { return Variadic(new Leaf()) + "/" + Variadic(new Leaf(), new Leaf()); }
        static int Callback() { return Evaluate(7, v -> v + 1); }
        static int Shadowed() {
            java.util.function.IntUnaryOperator Pick = value -> value * 5;
            return Pick.applyAsInt(4);
        }
    }

    public static void main(String[] args) {
        System.out.println(Child.Read());
        System.out.println(Child.Pick("left", "right"));
        var child = new Child();
        System.out.println(child.Lexical());
        System.out.println(child.Mixed());
        System.out.println(Child.Shadowed());
        System.out.println(Child.Narrowed());
        System.out.println(Child.Phase());
        System.out.println(Child.Callback());
    }
}`)
	classes := filepath.Join(root, "classes")
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"),
		"--release", strconv.Itoa(release), "-d", classes, reference)
	want := runObjectTestCommand(t, root, java, "-cp", classes, "Probe")

	base := filepath.Join(root, "parent.gpp")
	writeTestFile(t, base, `package reference.scope.parent

import "strconv"

class Root {}
class Middle : Root {}
class Leaf : Middle {}

class Parent {
    static func Pick(v int) int { return v + 1 }
    static func Pick(v string) string { return "parent:" + v }
    static func Pick(first string, last string) string { return first + "/" + last }
    static func Identity[T any](value T) T { return value }
    static func Narrow(value Root) string { return "root" }
    static func Narrow(value Middle) string { return "middle" }
    static func Variadic(value Root) string { return "fixed" }
    static func Variadic(value ...*Leaf) string { return "variadic:" + strconv.Itoa(len(value)) }
    static func Evaluate(value int, callback func(int) int) int { return callback(value) }
    static func MayFail(value int) (int, error) { return value, nil }
    static func MayFail(value string) string { return value }
    func Lexical() int { return Pick(10) }
}
`)
	child := filepath.Join(root, "child.gpp")
	writeTestFile(t, child, `package reference.scope.child

import "strconv"
import reference.scope.parent

func Native() string { return "go helper" }

class Child : parent.Parent {
    static func Pick(v int) int { return v + 20 }
    static Snapshot string = Pick("init")
    static func Pick(v bool) string {
        if v { return "on" }
        return "off"
    }
    static func Mix(v int) string { return "static:" + strconv.Itoa(v) }
    func Mix(v string) string { return "instance:" + v }
    static func Read() string { return Identity[string](Snapshot + "/" + strconv.Itoa(Pick(2)) + "/" + Pick(true)) }
    func Mixed() string { return Mix(3) + "/" + Mix("text") }
    static func Narrowed() string { return Narrow(parent.Leaf()) + "/" + Narrow(nil) }
    static func Phase() string { return Variadic(parent.Leaf()) + "/" + Variadic(parent.Leaf(), parent.Leaf()) }
    static func Callback() int { return Evaluate(7, v => v + 1) }
    static func Promoted() string { return strconv.Itoa(MayFail(9)) + "/" + MayFail("text") }
    static func Shadowed() int {
        Pick := func(value int) int { return value * 5 }
        return Pick(4)
    }
    static func Native() string { return "class member" }
    static func CheckPackageCall() string { return Native() + "/" + Child.Native() }
}

func Create() *Child { return Child() }

class SamePackageParent {
    static func Selected() int { return 5 }
    func Lexical() int { return Selected() }
}

class SamePackageChild : SamePackageParent {
    static func Selected() int { return 99 }
}

func CreateSamePackage() *SamePackageChild { return SamePackageChild() }
`)
	output := filepath.Join(root, "generated")
	module := filepath.Join(root, "entry.gpp")
	writeTestFile(t, module, `package main

import "fmt"
import reference.scope.child

func main() {
    fmt.Println(child.Child.Read())
    fmt.Println(child.Child.Pick("left", "right"))
    instance := child.Create()
    fmt.Println(instance.Lexical())
    fmt.Println(instance.Mixed())
    fmt.Println(child.Child.Shadowed())
    fmt.Println(child.Child.Narrowed())
    fmt.Println(child.Child.Phase())
    fmt.Println(child.Child.Callback())
    if child.Child.Promoted() != "9/text" { panic("selected error-bearing signature was lost") }
    if child.Child.CheckPackageCall() != "go helper/class member" { panic("Go helper lookup changed") }
    if child.CreateSamePackage().Lexical() != 5 { panic("same-package inherited body rebound its static call") }
}
`)
	if err := compiler.CompileFilesWithOptions([]string{base, child, module}, output,
		compiler.CompileOptions{ModulePath: "example.com/class-scope"}); err != nil {
		t.Fatal(err)
	}
	got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", ".")
	if got != want {
		t.Fatalf("GPP and Java static method selection diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}

func TestNativeGPPRejectsInstanceOverloadFromStaticContext(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	reference := filepath.Join(root, "Probe.java")
	writeTestFile(t, reference, `class Probe {
    static void Mix(int value) {}
    void Mix(String value) {}
    static void Call() { Mix("text"); }
}`)
	javac := exec.CommandContext(t.Context(), filepath.Join(filepath.Dir(java), "javac"),
		"--release", strconv.Itoa(release), "-d", root, reference)
	if output, err := javac.CombinedOutput(); err == nil || !strings.Contains(string(output), "non-static") {
		t.Fatalf("expected javac to reject the instance call: %v\n%s", err, output)
	}
	file, err := compiler.ParseFile("probe.gpp", `package main
class Probe {
    static func Mix(value int) {}
    func Mix(value string) {}
    static func Call() { Mix("text") }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = compiler.Emit(file)
	if err == nil || !strings.Contains(err.Error(), "instance method Probe.Mix cannot be called from a static context") {
		t.Fatalf("expected a static context diagnostic, got %v", err)
	}
}
