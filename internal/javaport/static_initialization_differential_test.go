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

func TestJavaAOTStaticInitializationMatchesOpenJDK(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	files := map[string]string{
		"Timeline.java": `package reference.statics.trace;
import reference.statics.model.Parent;
public final class Timeline {
    private Timeline() {}
    public static int log;
    public static int note(int digit) { log = log * 10 + digit; return digit; }
    public static int take() { int value = log; log = 0; return value; }
    public static Parent echo(Parent value) { return value; }
}`,
		"Parent.java": `package reference.statics.model;
import reference.statics.trace.Timeline;
public class Parent {
    public static final int MAGIC = 11;
    public static final String LABEL = "static";
    private static Object labelObject() { return LABEL; }
    public static int patternSize = labelObject() instanceof String text ? text.length() : -1;
    public static int early = readFuture();
    public static int value = Timeline.note(1);
    public static final int fixed;
    static {
        Timeline.note(2);
        fixed = value + early;
        if (labelObject() instanceof String text) patternSize += text.length();
    }
    public static int future = 40;
    private static int readFuture() { return future; }
    public static int labelLength() { return LABEL.length(); }
    public static int measure(Object value) {
        if (!(value instanceof String text)) return -1;
        return text.length();
    }
}`,
		"Child.java": `package reference.statics.model;
import reference.statics.trace.Timeline;
public final class Child extends Parent {
    public static int marker = Timeline.note(3);
}`,
		"Put.java": `package reference.statics.model;
import reference.statics.trace.Timeline;
public class Put { public static int value = Timeline.note(5); }`,
		"Call.java": `package reference.statics.model;
import reference.statics.trace.Timeline;
public class Call {
    static { Timeline.note(7); }
    public static int invoke(int a, int b) { Timeline.note(8); return a + b; }
}`,
		"Allocation.java": `package reference.statics.model;
import reference.statics.trace.Timeline;
public class Allocation {
    static { Timeline.note(4); }
    public Allocation(int ignored) { Timeline.note(6); }
}`,
		"Compound.java": `package reference.statics.model;
import reference.statics.trace.Timeline;
public class Compound { public static int value = Timeline.note(1); }`,
		"Concurrent.java": `package reference.statics.model;
import reference.statics.trace.Timeline;
public class Concurrent { public static int value = Timeline.note(6); }`,
		"Broken.java": `package reference.statics.model;
import reference.statics.trace.Timeline;
public class Broken {
    public static int value = fail();
    private static int fail() { Timeline.note(7); throw new IllegalArgumentException("failure"); }
}`,
		"BrokenChild.java": `package reference.statics.model;
import reference.statics.trace.Timeline;
public class BrokenChild extends Broken { public static int value = Timeline.note(9); }`,
		"DirectError.java": `package reference.statics.model;
public class DirectError {
    public static int value = fail();
    private static int fail() { throw new AssertionError("error"); }
}`,
		"Calls.java": `package reference.statics.probe;
import reference.statics.model.*;
import reference.statics.trace.Timeline;
public class Calls {
    private Child absentChild() { Timeline.note(9); return null; }
    private Put absentPut() { Timeline.note(3); return null; }
    private Compound absentCompound() { Timeline.note(2); return null; }
    private Call absentCall() { Timeline.note(5); return null; }
    public int passive() { return absentChild().MAGIC; }
    public boolean passiveTypes() {
        Object array = new Child[0];
        return array instanceof Child[];
    }
    public int inherited() { return absentChild().value; }
    public int childUse() { return Child.marker; }
    public int put() { return absentPut().value = Timeline.note(4); }
    public int method() { return absentCall().invoke(Timeline.note(6), Timeline.note(9)); }
    public Allocation allocation() { return new Allocation(Timeline.note(5)); }
    public int compound() { return absentCompound().value += Timeline.note(3); }
    public int postIncrement() { return absentCompound().value++; }
    public int failure() {
        try { return Broken.value; }
        catch (ExceptionInInitializerError failure) {
            if (failure.getCause() instanceof IllegalArgumentException) { return 71; }
            return 72;
        }
        catch (LinkageError failure) { return 73; }
    }
    public int childFailure() {
        try { return BrokenChild.value; }
        catch (NoClassDefFoundError failure) { return 74; }
    }
    public int directError() {
        try { return DirectError.value; }
        catch (AssertionError failure) { return 81; }
        catch (NoClassDefFoundError failure) { return 82; }
    }
}`,
	}
	var inputs []string
	for _, name := range slices.Sorted(maps.Keys(files)) {
		path := filepath.Join(root, name)
		writeTestFile(t, path, files[name])
		inputs = append(inputs, path)
	}
	probe := filepath.Join(root, "Probe.java")
	writeTestFile(t, probe, `import reference.statics.model.Parent;
import reference.statics.model.Concurrent;
import reference.statics.trace.Timeline;
import reference.statics.probe.Calls;
class Probe {
    public static void main(String[] args) {
        var calls = new Calls();
        System.out.println(calls.passive() + " " + Parent.LABEL + " " + calls.passiveTypes() + " " + Timeline.take());
        System.out.println(calls.inherited() + " " + Parent.early + " " + Parent.fixed + " " + Timeline.take());
        System.out.println(calls.childUse() + " " + Parent.labelLength() + " " + Timeline.take());
        System.out.println(Parent.patternSize + " " + Parent.measure(Parent.LABEL) + " " + Parent.measure(null));
        System.out.println(calls.put() + " " + Timeline.take() + " " + calls.method() + " " + Timeline.take());
        System.out.println((calls.allocation() != null) + " " + Timeline.take());
        System.out.println(calls.compound() + " " + Timeline.take() + " " + calls.postIncrement() + " " + Timeline.take());
        System.out.println(calls.failure() + " " + Timeline.take() + " " + calls.failure() + " " + calls.childFailure() + " " + Timeline.take());
        System.out.println(calls.directError() + " " + calls.directError());
        System.out.println(Concurrent.value + " " + Timeline.take());
        Parent.value = 20;
        System.out.println(Parent.value + " " + Timeline.take());
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
	var implementationCount int
	var emitted []string
	for _, unit := range units {
		if strings.HasPrefix(unit.Name, "internal.gppjavaport.") {
			implementationCount++
		}
		file := filepath.Join(root, unit.Name+".gpp")
		writeTestFile(t, file, unit.Source)
		emitted = append(emitted, file)
	}
	if implementationCount != 1 {
		t.Fatalf("expected a shared implementation for the cyclic static imports, got %d", implementationCount)
	}
	output := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions(emitted, output, compiler.CompileOptions{ModulePath: "example.com/java-statics"}); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(root, "external")
	if err := os.Mkdir(external, 0755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(external, "go.mod"), `module example.com/static-consumer

go 1.27

require example.com/java-statics v0.0.0

replace example.com/java-statics => ../generated
`)
	writeTestFile(t, filepath.Join(external, "main.go"), `package main

import (
    "fmt"
    "sync"
    "example.com/java-statics/reference/statics/model"
    "example.com/java-statics/reference/statics/probe"
    "example.com/java-statics/reference/statics/trace"
)

func main() {
    calls := probe.NewCalls()
    label, _ := model.ParentLABEL().ToGo()
    fmt.Println(calls.Passive(), label, calls.PassiveTypes(), trace.TimelineTake())
    fmt.Println(calls.Inherited(), model.GppGetStatic_Parent_early(), model.GppGetStatic_Parent_fixed(), trace.TimelineTake())
    fmt.Println(calls.ChildUse(), model.ParentLabelLength(), trace.TimelineTake())
    fmt.Println(model.GppGetStatic_Parent_patternSize(), model.ParentMeasure(model.ParentLABEL()), model.ParentMeasure(nil))
    fmt.Println(calls.Put(), trace.TimelineTake(), calls.Method(), trace.TimelineTake())
    fmt.Println(calls.Allocation() != nil, trace.TimelineTake())
    fmt.Println(calls.Compound(), trace.TimelineTake(), calls.PostIncrement(), trace.TimelineTake())
    fmt.Println(calls.Failure(), trace.TimelineTake(), calls.Failure(), calls.ChildFailure(), trace.TimelineTake())
    fmt.Println(calls.DirectError(), calls.DirectError())
    var readers sync.WaitGroup
    for range 32 {
        readers.Go(func() {
            if value := model.GppGetStatic_Concurrent_value(); value != 6 {
                panic("class initializer published an incorrect value")
            }
        })
    }
    readers.Wait()
    fmt.Println(model.GppGetStatic_Concurrent_value(), trace.TimelineTake())
    model.GppSetStatic_Parent_value(20)
    fmt.Println(model.GppGetStatic_Parent_value(), trace.TimelineTake())
}
`)
	got := runObjectTestCommand(t, external, "go", "run", "-buildvcs=false", ".")
	if want != got {
		t.Fatalf("Go and OpenJDK static initialization diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}
