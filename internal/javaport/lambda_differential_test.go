package javaport

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestJavaLambdasMatchOpenJDK(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	files := map[string]string{
		"Action.java": `package reference.lambdas.api;
@FunctionalInterface
public interface Action {
    int apply(int value);
    default int twice(int value) { return apply(value) + apply(value); }
}`,
		"Tagged.java": `package reference.lambdas.api;
public interface Tagged { default int tag() { return 42; } }`,
		"Extended.java": `package reference.lambdas.api;
@FunctionalInterface
public interface Extended extends Action {}`,
		"Worker.java": `package reference.lambdas.api;
@FunctionalInterface
public interface Worker { void run(); }`,
		"Boxed.java": `package reference.lambdas.api;
public interface Boxed { Integer apply(Integer value); }`,
		"Wide.java": `package reference.lambdas.api;
public interface Wide { long apply(Integer value); }`,
		"HostCall.java": `package reference.lambdas.api;
import reference.lambdas.host.Host;
public interface HostCall { int apply(Host receiver, int delta); }`,
		"HostMaker.java": `package reference.lambdas.api;
import reference.lambdas.host.Host;
public interface HostMaker { Host make(int value); }`,
		"ArrayMaker.java": `package reference.lambdas.api;
public interface ArrayMaker { int[] make(int length); }`,
		"TextSource.java": `package reference.lambdas.api;
public interface TextSource { String get(); }`,
		"Named.java": `package reference.lambdas.api;
public interface Named { default String name() { return "default"; } }`,
		"Parent.java": `package reference.lambdas.host;
public class Parent { public int offset(int change) { return 100 + change; } }`,
		"Host.java": `package reference.lambdas.host;
import reference.lambdas.api.*;
public class Host extends Parent implements Named {
    private int value;
    private int selections;
    private Action eager = n -> n + this.value;
    public Host(int initial) { value = initial; }
    public int current() { return value; }
    private int save(int next) { value = next; return value; }
    public Action eager() { return eager; }
    public Worker worker(int delta) { return () -> save(value + delta); }
    public Extended capture(int base) {
        int local = base * 2;
        return n -> n + this.value + base + local;
    }
    public Action nested(int outer) {
        return left -> {
            Action inner = right -> left + right + outer;
            return inner.apply(4);
        };
    }
    public Action block(int base) {
        return (int n) -> {
            try {
                int selected = n + base;
                if (selected == 4) { throw new IllegalArgumentException(); }
                return selected;
            } catch (IllegalArgumentException failure) {
                return 41;
            } finally {
                value += 1;
            }
        };
    }
    public Action text() { return i -> "abc".charAt(i); }
    public Boxed boxed() { return n -> n + value; }
    public Wide widened() { return n -> n; }
    public int nullUnbox() {
        try { return (int) widened().apply(null); }
        catch (NullPointerException failure) { return -5; }
    }
    public Action reference(int[] array) { return i -> array[i]; }
    public Action pattern(Object candidate) {
        if (candidate instanceof Host other) { return n -> other.value + n; }
        return n -> 0;
    }
    public int patternLoop(Object[] candidates) {
        Action[] sinks = new Action[2];
        int index = 0;
        for (Object candidate : candidates) {
            if (candidate instanceof Host other) {
                sinks[index] = n -> other.value + n;
                index += 1;
            }
        }
        return sinks[0].apply(1) * 10 + sinks[1].apply(2);
    }
    public Action catchCapture() {
        try { throw new IllegalArgumentException("capture"); }
        catch (IllegalArgumentException failure) { return i -> failure.getMessage().length() + i; }
    }
    public Action intersection(int offset) {
        var both = (Action & Tagged) (n -> n + offset);
        return both;
    }
    public int tag(Object object) {
        return ((Action & Tagged) object).tag();
    }
    public boolean same(Action value, Object other) { return value == other; }
    public int applyNative(Action function, int value) { return function.apply(value); }
    public int offset(int bias) { return value + bias; }
    public static int tripled(int value) { return value * 3; }
    private Host select(Host receiver) { selections += 1; return receiver; }
    public int selections() { return selections; }
    public Action staticReference() { return Host::tripled; }
    public Action boundReference(Host receiver) { return select(receiver)::offset; }
    public Action thisReference() { return this::offset; }
    public Action superReference() { return super::offset; }
    public HostCall unboundReference() { return Host::offset; }
    public HostMaker constructorReference() { return Host::new; }
    public ArrayMaker arrayReference() { return int[]::new; }
    public Wide convertingReference() { return Host::tripled; }
    private int increment() { value += 1; return value; }
    public Worker voidReference() { return this::increment; }
    public TextSource caption(String prefix) { return () -> prefix + ":" + value; }
    public TextSource objectText(Object receiver) { return receiver::toString; }
    public TextSource defaultText() { return Named.super::name; }
    @Override public String toString() { return "Host:" + value; }
    public int nullReceiverReference() {
        try { Action delayed = select(null)::offset; return 0; }
        catch (NullPointerException failure) { return selections; }
    }
}`,
	}
	var inputs []string
	for _, name := range slices.Sorted(maps.Keys(files)) {
		file := filepath.Join(root, name)
		writeTestFile(t, file, files[name])
		inputs = append(inputs, file)
	}
	probe := filepath.Join(root, "Probe.java")
	writeTestFile(t, probe, `import reference.lambdas.host.Host;
class Probe {
    public static void main(String[] args) {
        var host = new Host(10);
        var captured = host.capture(3);
        var update = host.worker(2);
        System.out.println(host.eager().apply(2) + " " + captured.apply(4) + " " + captured.twice(4));
        update.run();
        System.out.println(host.current() + " " + captured.apply(4) + " " + host.nested(2).apply(3));
        var block = host.block(2);
        System.out.println(block.apply(2) + " " + block.apply(7) + " " + host.current() + " " + (int) host.text().apply(1));
        System.out.println(host.boxed().apply(3) + " " + host.widened().apply(-18) + " " + host.nullUnbox());
        int[] array = {7};
        var ref = host.reference(array);
        array[0] = 99;
        System.out.println(ref.apply(0) + " " + host.pattern(new Host(20)).apply(2) + " " +
            host.pattern(null).apply(2) + " " + host.catchCapture().apply(2) + " " +
            host.patternLoop(new Object[] {new Host(5), new Host(7)}));
        var both = host.intersection(6);
        System.out.println(both.apply(7) + " " + host.tag(both) + " " + host.same(both, both) + " " +
            host.same(both, host.intersection(8)) + " " + host.applyNative(n -> n * 5, 8));
        var bound = host.boundReference(host);
        int selections = host.selections();
        host.voidReference().run();
        System.out.println(host.staticReference().apply(5) + " " + bound.apply(3) + " " + bound.apply(8) +
            " " + host.thisReference().apply(1) + " " + host.unboundReference().apply(new Host(51), 2) + " " +
            host.constructorReference().make(22).current() + " " + host.arrayReference().make(5).length + " " +
            host.convertingReference().apply(4) + " " + selections + " " + host.nullReceiverReference());
        System.out.println(host.superReference().apply(7) + " " + host.caption("value").get() + " " +
            host.objectText(host).get() + " " + host.defaultText().get());
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
	for _, unit := range units {
		path := filepath.Join(root, unit.Name+".gpp")
		writeTestFile(t, path, unit.Source)
		sources = append(sources, path)
	}
	output := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions(sources, output,
		compiler.CompileOptions{ModulePath: "example.com/java-lambdas"}); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(root, "external")
	if err := os.Mkdir(external, 0755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(external, "go.mod"), `module example.com/lambda-consumer

go 1.27

require example.com/java-lambdas v0.0.0

replace example.com/java-lambdas => ../generated
`)
	writeTestFile(t, filepath.Join(external, "main.go"), `package main

import (
    "fmt"
    java "example.com/java-lambdas/gpp/java"
    "example.com/java-lambdas/reference/lambdas/api"
    "example.com/java-lambdas/reference/lambdas/host"
)

type nativeMultiplier struct{ factor int32 }

func (native *nativeMultiplier) Apply(value int32) int32 { return value * native.factor }
func (native *nativeMultiplier) Twice(value int32) int32 { return native.Apply(value) * 2 }
var _ api.Action = (*nativeMultiplier)(nil)

func text(value *java.String) string { result, _ := value.ToGo(); return result }

func main() {
    model := host.NewHostWithInt(10)
    var captured api.Extended = model.Capture(3)
    update := model.Worker(2)
    fmt.Println(model.Eager().Apply(2), captured.Apply(4), captured.Twice(4))
    update.Run()
    fmt.Println(model.Current(), captured.Apply(4), model.Nested(2).Apply(3))
    block := model.Block(2)
    fmt.Println(block.Apply(2), block.Apply(7), model.Current(), model.Text().Apply(1))
    fmt.Println(java.UnboxInteger(model.Boxed().Apply(java.BoxInteger(3))),
        model.Widened().Apply(java.BoxInteger(-18)), model.NullUnbox())
    array := java.NewArray[int32](1, []int32{1})
    java.ArraySet[int32](array, 0, 7)
    ref := model.Reference(array)
    java.ArraySet[int32](array, 0, 99)
    candidates := java.NewArray[any](1, []int32{2})
    java.ArraySet[any](candidates, 0, host.NewHostWithInt(5))
    java.ArraySet[any](candidates, 1, host.NewHostWithInt(7))
    fmt.Println(ref.Apply(0), model.Pattern(host.NewHostWithInt(20)).Apply(2),
        model.Pattern(nil).Apply(2), model.CatchCapture().Apply(2), model.PatternLoop(candidates))
    both := model.Intersection(6)
    fmt.Println(both.Apply(7), model.Tag(both), model.Same(both, both),
        model.Same(both, model.Intersection(8)), model.ApplyNative(&nativeMultiplier{factor: 5}, 8))
    bound := model.BoundReference(model)
    selections := model.Selections()
    model.VoidReference().Run()
    fmt.Println(model.StaticReference().Apply(5), bound.Apply(3), bound.Apply(8), model.ThisReference().Apply(1),
        model.UnboundReference().Apply(host.NewHostWithInt(51), 2), model.ConstructorReference().Make(22).Current(),
        java.ArrayLength(model.ArrayReference().Make(5)), model.ConvertingReference().Apply(java.BoxInteger(4)),
        selections, model.NullReceiverReference())
    fmt.Println(model.SuperReference().Apply(7), text(model.Caption(java.StringFromGo("value")).Get()),
        text(model.ObjectText(model.GppAsParent()).Get()), text(model.DefaultText().Get()))
}
`)
	got := runObjectTestCommand(t, external, "go", "run", "-buildvcs=false", ".")
	if want != got {
		t.Fatalf("Go and OpenJDK lambda semantics diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}
