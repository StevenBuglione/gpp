package javaport

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestJavaInterfacesMatchOpenJDK(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	sources := map[string]string{
		"Counter.java": `package reference.interfaces.contract;
import reference.interfaces.model.Base;
public interface Counter {
    int BONUS = 100;
    String LABEL = "service";
    int score(int delta);
    default int total() { return score(2) + BONUS; }
    default int classWinner() { return 41; }
    default Counter identity() { return this; }
    default Base create(int seed) { return new Base(seed); }
    default int labelLength() { return LABEL.length(); }
    default int asserted(int value) {
        return switch (value) {
            case 0 -> { assert false : "interface default"; yield 1; }
            default -> 2;
        };
    }
}`,
		"Other.java": `package reference.interfaces.contract;
public interface Other { int score(int delta); }`,
		"External.java": `package reference.interfaces.probe;
public interface External { int apply(int number); }`,
		"Foreign.java": `package reference.interfaces.probe;
public class Foreign implements External { public int apply(int number) { return number + 40; } }`,
		"Base.java": `package reference.interfaces.model;
import reference.interfaces.contract.Counter;
public class Base implements Counter {
    private int seed;
    private final int during;
    public Base(int seed) { this.seed = seed; during = total(); }
    public int during() { return during; }
    @Override public int score(int delta) { return seed + delta; }
    @Override public int classWinner() { return 77; }
}`,
		"Strong.java": `package reference.interfaces.advanced;
import reference.interfaces.contract.Counter;
public interface Strong extends Counter {
    @Override default int total() { return Counter.super.total() + 1000; }
    @Override default int classWinner() { return 42; }
}`,
		"Child.java": `package reference.interfaces.advanced;
import reference.interfaces.contract.Other;
import reference.interfaces.model.Base;
public final class Child extends Base implements Strong, Other {
    private int childSeed = 7;
    public Child() { super(5); }
    @Override public int score(int delta) { return childSeed + delta; }
    public int explicitDefault() { return Strong.super.total(); }
}`,
		"Mimic.java": `package reference.interfaces.probe;
public class Mimic { public int apply(int number) { return number + 99; } }`,
		"Calls.java": `package reference.interfaces.probe;
import reference.interfaces.advanced.Child;
import reference.interfaces.advanced.Strong;
import reference.interfaces.contract.Counter;
import reference.interfaces.contract.Other;
import reference.interfaces.model.Base;
public class Calls {
    private int times;
    private int next() { times = times + 1; return times; }
    public Counter widen(Base value) { return value; }
    public Strong toStrong(Counter value) { return (Strong) value; }
    public Base toBase(Counter value) { return (Base) value; }
    public Object objectView(Counter value) { return value; }
    public boolean crossEquality(Counter left, Other right) { return left == right; }
    public boolean inheritedTest(Object value) { return value instanceof Counter; }
    public boolean strongTest(Object value) { return value instanceof Strong; }
    public int dispatch(Counter value) { return value.total(); }
    public int checkDefault(Counter value) {
        try { return value.asserted(0); }
        catch (AssertionError failure) { return 91; }
    }
    public int callGo(External value) {
        times = 0;
        try { return value.apply(next()); }
        catch (NullPointerException failure) { return times * 10 + 5; }
    }
    public boolean externalTest(Object value) { return value instanceof External; }
    public int badCast(Object value) {
        try { return ((External) value).apply(1); }
        catch (ClassCastException failure) { return -3; }
    }
    public boolean testCounterArray(Object value) { return value instanceof Counter[]; }
    public boolean testExternalArray(Object value) { return value instanceof External[]; }
    public External[] externals() { return new External[1]; }
    public Base[] bases() { return new Base[1]; }
    public Mimic[] mimics() { return new Mimic[1]; }
    public int badStore() {
        Object[] values = new External[1];
        try { values[0] = new Mimic(); return 0; }
        catch (ArrayStoreException failure) { return -4; }
    }
}`,
	}
	var inputs []string
	for _, name := range []string{"Counter", "Other", "External", "Foreign", "Base", "Strong", "Child", "Mimic", "Calls"} {
		file := filepath.Join(root, name+".java")
		writeTestFile(t, file, sources[name+".java"])
		inputs = append(inputs, file)
	}
	probe := filepath.Join(root, "Probe.java")
	writeTestFile(t, probe, `import reference.interfaces.advanced.Child;
import reference.interfaces.contract.Counter;
import reference.interfaces.model.Base;
import reference.interfaces.probe.Calls;
import reference.interfaces.probe.Foreign;
import reference.interfaces.probe.Mimic;
class Probe {
    public static void main(String[] args) {
        var calls = new Calls();
        var loader = Counter.class.getClassLoader();
        loader.setClassAssertionStatus(Counter.class.getName(), true);
        var base = new Base(5);
        var child = new Child();
        loader.setClassAssertionStatus(Counter.class.getName(), false);
        Counter viewed = calls.widen(child);
        System.out.println(base.during() + " " + child.during() + " " + child.explicitDefault() + " " +
            calls.dispatch(base) + " " + calls.dispatch(child) + " " + viewed.score(1) + " " +
            viewed.create(9).score(1) + " " + viewed.labelLength() + " " + viewed.classWinner());
        System.out.println((viewed.identity() == child) + " " + (calls.toStrong(viewed) == child) + " " +
            (calls.toBase(viewed) == child) + " " + (calls.objectView(viewed) == child) + " " +
            calls.crossEquality(viewed, child) + " " + calls.inheritedTest(child) + " " +
            calls.strongTest(base) + " " + calls.strongTest(null));
        var mimic = new Mimic();
        var foreign = new Foreign();
        System.out.println(calls.callGo(foreign) + " " + calls.callGo(null) + " " + calls.externalTest(foreign) + " " +
            calls.externalTest(mimic) + " " + calls.badCast(mimic));
        System.out.println(calls.testCounterArray(calls.bases()) + " " + calls.testExternalArray(calls.mimics()) +
            " " + calls.testExternalArray(calls.externals()) + " " + calls.badStore() + " " + Counter.LABEL);
        System.out.println(calls.checkDefault(viewed));
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
	var sourcesGPP []string
	var implementations int
	for _, unit := range units {
		if strings.HasPrefix(unit.Name, "internal.gppjavaport.") {
			implementations++
		}
		file := filepath.Join(root, unit.Name+".gpp")
		writeTestFile(t, file, unit.Source)
		sourcesGPP = append(sourcesGPP, file)
	}
	if implementations != 1 {
		t.Fatalf("expected Counter and Base in one cycle implementation, got %d", implementations)
	}
	output := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions(sourcesGPP, output, compiler.CompileOptions{
		ModulePath: "example.com/java-interfaces",
	}); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(root, "external")
	if err := os.Mkdir(external, 0755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(external, "go.mod"), `module example.com/java-interface-consumer

go 1.27

require example.com/java-interfaces v0.0.0

replace example.com/java-interfaces => ../generated
`)
	writeTestFile(t, filepath.Join(external, "main.go"), `package main

import (
    "fmt"
    javart "example.com/java-interfaces/gpp/java"
    "example.com/java-interfaces/reference/interfaces/advanced"
    "example.com/java-interfaces/reference/interfaces/contract"
    "example.com/java-interfaces/reference/interfaces/model"
    "example.com/java-interfaces/reference/interfaces/probe"
)

type foreign struct{ seed int32 }

func (native *foreign) Apply(number int32) int32 { return number + native.seed }

var _ probe.External = (*foreign)(nil)
var _ contract.Counter = (*model.Base)(nil)
var _ advanced.Strong = (*advanced.Child)(nil)

func main() {
    calls := probe.NewCalls()
    javart.SetClassAssertionStatus("reference.interfaces.contract.Counter", true)
    base := model.NewBaseWithInt(5)
    child := advanced.NewChild()
    javart.SetClassAssertionStatus("reference.interfaces.contract.Counter", false)
    baseView := child.GppAsBase()
    var viewed contract.Counter = calls.Widen(baseView)
    fmt.Println(base.During(), child.During(), child.ExplicitDefault(),
        calls.Dispatch(base), calls.Dispatch(baseView), viewed.Score(1), viewed.Create(9).Score(1), viewed.LabelLength(),
        viewed.ClassWinner())
    fmt.Println(viewed.Identity() == child, calls.ToStrong(viewed) == child, calls.ToBase(viewed) == baseView,
        calls.ObjectView(viewed) == child, calls.CrossEquality(viewed, child), calls.InheritedTest(baseView),
        calls.StrongTest(base), calls.StrongTest((*advanced.Child)(nil)))
    native := &foreign{seed: 40}
    mimic := probe.NewMimic()
    fmt.Println(calls.CallGo(native), calls.CallGo((*foreign)(nil)), calls.ExternalTest(native),
        calls.ExternalTest(mimic), calls.BadCast(mimic))
    label, _ := contract.CounterLABEL().ToGo()
    fmt.Println(calls.TestCounterArray(calls.Bases()), calls.TestExternalArray(calls.Mimics()),
        calls.TestExternalArray(calls.Externals()), calls.BadStore(), label)
    fmt.Println(calls.CheckDefault(viewed))
}
`)
	got := runObjectTestCommand(t, external, "go", "run", "-buildvcs=false", ".")
	if want != got {
		t.Fatalf("Go and OpenJDK interface semantics diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}
