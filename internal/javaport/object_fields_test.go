package javaport

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestJavaQualifiedInstanceFieldsMatchOpenJDK(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	base := filepath.Join(root, "Base.java")
	child := filepath.Join(root, "Child.java")
	writeTestFile(t, base, `package reference.fields.model;
public class Base {
    public int value = 5;
    protected int inherited = 20;
    private int hidden = 7;
    public final int seal;
    public Base() { seal = 40; }
    public int readPeer(Base other) { return other.hidden; }
}`)
	writeTestFile(t, child, `package reference.fields.client;
import reference.fields.model.Base;
public class Child extends Base {
    public int value = 9;
    private int trace;
    public Child() { super(); }
    private Base mark(Base target) { trace = trace * 10 + 1; return target; }
    private int right() { trace = trace * 10 + 2; return 6; }
    public int trace() { return trace; }
    public int choose(Base base, Child child) {
        return base.value * 1000 + child.value * 100 + super.value * 10 + value;
    }
    public int readFinal(Base base) { return base.seal; }
    public int updateProtected(Child other) { other.inherited += 2; return inherited + other.inherited; }
    public int relocate(Base base, Base replacement) {
        Base original = base;
        (base.value) = (base = replacement).value;
        return original.value * 100 + base.value;
    }
    public int store(Base base) {
        trace = 0;
        try { return mark(base).value = right(); }
        catch (NullPointerException failure) { return -trace; }
    }
    public int compound(Base base) {
        trace = 0;
        try { return mark(base).value += right(); }
        catch (NullPointerException failure) { return -trace; }
    }
    public int increment(Base base) {
        trace = 0;
        try { return mark(base).value++ + right(); }
        catch (NullPointerException failure) { return -trace; }
    }
    public int read(Base base) {
        trace = 0;
        try { return mark(base).value + right(); }
        catch (NullPointerException failure) { return -trace; }
    }
}`)
	probe := filepath.Join(root, "Probe.java")
	writeTestFile(t, probe, `import reference.fields.model.Base;
import reference.fields.client.Child;
class Probe {
    public static void main(String[] args) {
        var subject = new Child();
        var peer = new Child();
        var replacement = new Base();
        Base base = peer;
        System.out.println(subject.choose(base, peer) + " " + base.readPeer(replacement) + " " +
            subject.readFinal(base) + " " + subject.updateProtected(peer));
        replacement.value = 35;
        System.out.println(subject.relocate(base, replacement) + " " + base.value + " " + peer.value);
        System.out.println(subject.store(null) + " " + subject.compound(null) + " " +
            subject.increment(null) + " " + subject.read(null));
        int stored = subject.store(base);
        int storeTrace = subject.trace();
        int updated = subject.compound(base);
        int updateTrace = subject.trace();
        int previous = subject.increment(base);
        System.out.println(stored + " " + storeTrace + " " + updated + " " + updateTrace + " " +
            previous + " " + subject.trace() + " " + base.value);
    }
}`)
	classes := filepath.Join(root, "classes")
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"),
		"--release", strconv.Itoa(release), "-d", classes, base, child, probe)
	want := runObjectTestCommand(t, root, java, "-cp", classes, "Probe")

	report, err := Analyze(t.Context(), []string{base, child}, Options{Java: java, Release: release, MethodBodies: true})
	if err != nil {
		t.Fatal(err)
	}
	units, err := EmitGPPPackages(report)
	if err != nil {
		t.Fatal(err)
	}
	var inputs []string
	for _, unit := range units {
		input := filepath.Join(root, unit.Name+".gpp")
		writeTestFile(t, input, unit.Source)
		inputs = append(inputs, input)
	}
	output := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions(inputs, output, compiler.CompileOptions{
		ModulePath: "example.com/java-fields",
	}); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(output, "main.go"), `package main

import (
    "fmt"

    "example.com/java-fields/reference/fields/client"
    "example.com/java-fields/reference/fields/model"
)

func main() {
    subject := client.NewChild()
    peer := client.NewChild()
    replacement := model.NewBase()
    base := peer.GppAsBase()
    fmt.Println(subject.Choose(base, peer), base.ReadPeer(replacement), subject.ReadFinal(base), subject.UpdateProtected(peer))
    if _, writable := any(base).(interface{ GppJavaSetField_Base_seal(int32) }); writable {
        panic("expected a read-only Go bridge for the final field")
    }
    replacement.GppJavaSetField_Base_value(35)
    fmt.Println(subject.Relocate(base, replacement), base.GppJavaGetField_Base_value(), peer.GppJavaGetField_Child_value())
    fmt.Println(subject.Store(nil), subject.Compound(nil), subject.Increment(nil), subject.Read(nil))
    stored := subject.Store(base)
    storeTrace := subject.Trace()
    updated := subject.Compound(base)
    updateTrace := subject.Trace()
    previous := subject.Increment(base)
    fmt.Println(stored, storeTrace, updated, updateTrace, previous, subject.Trace(), base.GppJavaGetField_Base_value())
}
`)
	if got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", "."); got != want {
		t.Fatalf("qualified Java instance fields diverged\nGo:\n%s\nOpenJDK:\n%s", got, want)
	}
}
