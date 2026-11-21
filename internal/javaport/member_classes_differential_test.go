package javaport

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestJavaMemberClassesMatchOpenJDK(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	files := map[string]string{
		"Outer.java": `package reference.members.host;
import reference.members.child.Qualified;
import reference.members.calls.Calls;
import reference.members.api.Function;
public class Outer {
    private int value;
    public Outer(int value) { this.value = value; }
    private int add(int increment) { return value + increment; }
    public void setValue(int value) { this.value = value; }
    public Inner make(int number) { return new Inner(number); }
    public Derived derived(int number) { return new Derived(number); }
    public Qualified cycle(Qualified value) { return value; }
    public static class Tool {
        private static int shared = 10;
        private final int data;
        public Tool(int data) { this.data = data; }
        public int read() { return data + shared; }
        public static int state() { return shared; }
    }
    public record Count(int number) {}
    public class Inner {
        private int local;
        private int initialOuter = value;
        public Inner(int number) { this(number, 1); }
        protected Inner(int number, int bonus) { local = number + bonus + value; }
        public int report() { return add(local); }
        public int explicit() { return Outer.this.add(local + 1); }
        public int initialOuter() { return initialOuter; }
        public Outer outer() { return Outer.this; }
        public Function function() { return delta -> add(local + delta); }
        public Deep deeper() { return new Deep(); }
        public class Deep {
            public int report() { return Outer.this.add(Inner.this.local); }
            public Outer outer() { return Outer.this; }
            public Inner immediate() { return Inner.this; }
        }
    }
    public class Derived extends Inner {
        public Derived(int number) { super(number); }
        @Override public int report() { return super.report() + 100; }
    }
    public class Shadow extends Outer {
        public Shadow() { super(90); }
        public int lexicalPrivate() { return value; }
        public int instancePrivate() { return ((Outer) this).add(0); }
    }
    public class Late {
        static { Calls.note(2); }
        public Late(int ignored) { Calls.note(4); }
    }
}`,
		"Outer_Tool.java": `package reference.members.host;
public class Outer_Tool { public int value() { return 39; } }`,
		"Function.java": `package reference.members.api;
public interface Function { int apply(int value); }`,
		"Shelf.java": `package reference.members.api;
public interface Shelf {
    class Marker { public int value() { return 29; } }
}`,
		"Qualified.java": `package reference.members.child;
import reference.members.host.Outer;
public final class Qualified extends Outer.Inner {
    public Qualified(Outer owner, int number) { owner.super(number, 2); }
    @Override public int report() { return super.report() + 1000; }
}`,
		"Calls.java": `package reference.members.calls;
import reference.members.host.Outer;
public class Calls {
    private static int events;
    public static int note(int digit) { events = events * 10 + digit; return digit; }
    private Outer select(Outer value) { note(1); return value; }
    public int nullOrder() {
        events = 0;
        try { select(null).new Late(note(3)); return 0; }
        catch (NullPointerException failure) { return events; }
    }
    public int creationOrder(Outer outer) {
        events = 0;
        select(outer).new Late(note(3));
        return events;
    }
    public Outer.Inner[] members(Outer one, Outer two) {
        return new Outer.Inner[] {one.new Inner(3), two.make(6)};
    }
    public int accept(Outer.Inner value) { return value.report(); }
}`,
	}
	var inputs []string
	for _, name := range slices.Sorted(maps.Keys(files)) {
		path := filepath.Join(root, name)
		writeTestFile(t, path, files[name])
		inputs = append(inputs, path)
	}
	probe := filepath.Join(root, "Probe.java")
	writeTestFile(t, probe, `import reference.members.api.Shelf;
import reference.members.calls.Calls;
import reference.members.child.Qualified;
import reference.members.host.Outer;
import reference.members.host.Outer_Tool;
class Probe {
    public static void main(String[] args) {
        var first = new Outer(7);
        var second = new Outer(12);
        var calls = new Calls();
        System.out.println(new Outer.Tool(3).read() + " " + Outer.Tool.state() + " " +
            new Outer_Tool().value() + " " + new Outer.Count(21).number() + " " + new Shelf.Marker().value());
        var inner = first.make(4);
        var deep = inner.deeper();
        var function = inner.function();
        System.out.println(inner.report() + " " + inner.explicit() + " " + inner.initialOuter() +
            " " + deep.report() + " " + (deep.outer() == first) + " " + (deep.immediate() == inner));
        first.setValue(20);
        System.out.println(function.apply(3));
        var shadow = first.new Shadow();
        System.out.println(shadow.lexicalPrivate() + " " + shadow.instancePrivate());
        var derived = first.derived(6);
        var qualified = new Qualified(second, 5);
        System.out.println(calls.accept(derived) + " " + calls.accept(qualified) + " " +
            (qualified.outer() == second) + " " + (first.cycle(qualified) == qualified));
        var members = calls.members(first, second);
        System.out.println(members[0].report() + " " + members[1].report() + " " + calls.nullOrder() + " " +
            calls.creationOrder(first));
    }
}`)
	classes := filepath.Join(root, "classes")
	args := append([]string{"--release", strconv.Itoa(release), "-d", classes, probe}, inputs...)
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"), args...)
	want := runObjectTestCommand(t, root, java, "-cp", classes, "Probe")
	report, err := Analyze(t.Context(), inputs, Options{Java: java, Release: release, MethodBodies: true})
	if err != nil {
		t.Fatal(err)
	}
	units, err := EmitGPPPackages(report)
	if err != nil {
		t.Fatal(err)
	}
	var sources []string
	var groups int
	for _, unit := range units {
		if strings.HasPrefix(unit.Name, "internal.gppjavaport.") {
			groups++
		}
		path := filepath.Join(root, unit.Name+".gpp")
		writeTestFile(t, path, unit.Source)
		sources = append(sources, path)
	}
	if groups != 1 {
		t.Fatalf("expected a shared implementation for the mutually dependent Java packages, got %d", groups)
	}
	output := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions(sources, output,
		compiler.CompileOptions{ModulePath: "example.com/java-members"}); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(root, "external")
	if err := os.Mkdir(external, 0755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(external, "go.mod"), `module example.com/member-consumer

go 1.27

require example.com/java-members v0.0.0

replace example.com/java-members => ../generated
`)
	writeTestFile(t, filepath.Join(external, "main.go"), `package main

import (
    "fmt"
    java "example.com/java-members/gpp/java"
    "example.com/java-members/reference/members/api"
    "example.com/java-members/reference/members/calls"
    "example.com/java-members/reference/members/child"
    "example.com/java-members/reference/members/host"
)

func main() {
    first := host.NewOuterWithInt(7)
    second := host.NewOuterWithInt(12)
    invoke := calls.NewCalls()
    fmt.Println(host.NewOuter_Tool_2WithInt(3).Read(), host.Outer_Tool_2State(),
        host.NewOuter_Tool().Value(), host.NewOuter_CountWithInt(21).Number(), api.NewShelf_Marker().Value())
    var inner *host.Outer_Inner = first.Make(4)
    var deep *host.Outer_Inner_Deep = inner.Deeper()
    function := inner.Function()
    fmt.Println(inner.Report(), inner.Explicit(), inner.InitialOuter(), deep.Report(), deep.Outer() == first, deep.Immediate() == inner)
    first.SetValue(20)
    fmt.Println(function.Apply(3))
    shadow := host.NewOuter_Shadow(first)
    fmt.Println(shadow.LexicalPrivate(), shadow.InstancePrivate())
    derived := first.Derived(6)
    qualified := child.NewQualifiedWithOuterAndInt(second, 5)
    fmt.Println(invoke.Accept(derived.GppAsOuter_Inner()), invoke.Accept(qualified.GppAsOuter_Inner()),
        qualified.Outer() == second, first.Cycle(qualified) == qualified)
    members := invoke.Members(first, second)
    fmt.Println(java.ArrayGet[*host.Outer_Inner](members, 0).Report(), java.ArrayGet[*host.Outer_Inner](members, 1).Report(),
        invoke.NullOrder(), invoke.CreationOrder(first))
    if direct := host.NewOuter_InnerWithInt(second, 6); direct.Outer() != second || direct.Report() != 31 {
        panic("the Go member constructor lost its enclosing receiver")
    }
}
`)
	got := runObjectTestCommand(t, external, "go", "run", "-buildvcs=false", ".")
	if want != got {
		t.Fatalf("Go and OpenJDK member classes diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}
