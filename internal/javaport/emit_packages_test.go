package javaport

import (
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestJavaPackagesMatchReference(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	sources := map[string]string{
		"Base.java": `package reference.foundation.shared;
public class Base {
    private final int seen = read();
    private final int observed = score();
    private final int seed;
    protected Base(int seed) { this.seed = seed; }
    public int read() { return 100; }
    public int score() { return 80; }
    public int seen() { return seen; }
    public int observed() { return observed; }
    public int seed() { return seed; }
}`,
		"Middle.java": `package reference.extension.shared;
import reference.foundation.shared.Base;
public class Middle extends Base {
    private int live = 33;
    public Middle(int seed) { super(seed); }
    @Override public int read() { return 50; }
    @Override public int score() { return live; }
    public int baseRead() { return super.read(); }
}`,
		"Child.java": `package reference.plugins;
import reference.extension.shared.Middle;
public final class Child extends Middle {
    private int ready = 42;
    public Child(int seed) { super(seed); }
    @Override public int read() { return ready; }
    public int middleRead() { return super.read(); }
}`,
		"Facade.java": `package reference.consumer;
import reference.foundation.shared.Base;
import reference.extension.shared.Middle;
import reference.plugins.Child;
public class Facade {
    public Base create(int seed) { return new Child(seed); }
    public int compare(Middle middle, Base base) { return middle.read() * 10 + base.score(); }
    public boolean same(Base base, Child child) { return base == child; }
    public int handleNull(Middle middle) {
        try { return middle.read(); }
        catch (NullPointerException ignored) { return 98; }
    }
}`,
	}
	var paths []string
	for _, name := range slices.Sorted(maps.Keys(sources)) {
		path := filepath.Join(root, name)
		writeTestFile(t, path, sources[name])
		paths = append(paths, path)
	}
	probe := filepath.Join(root, "Probe.java")
	writeTestFile(t, probe, `import reference.consumer.Facade;
import reference.extension.shared.Middle;
import reference.foundation.shared.Base;
import reference.plugins.Child;
class Probe {
    public static void main(String[] args) {
        var facade = new Facade();
        var child = new Child(19);
        Base base = child;
        Middle middle = child;
        System.out.println(base.seen() + " " + base.observed() + " " + base.seed());
        System.out.println(base.read() + " " + middle.score() + " " + child.middleRead() + " " + child.baseRead());
        System.out.println(facade.create(13).read() + " " + facade.create(23).seed());
        System.out.println(facade.compare(middle, base) + " " + facade.same(base, child) + " " + facade.handleNull(null));
    }
}`)
	classes := filepath.Join(root, "classes")
	javacArgs := append([]string{"--release", strconv.Itoa(release), "-d", classes, probe}, paths...)
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"), javacArgs...)
	want := runObjectTestCommand(t, root, java, "-cp", classes, "Probe")
	report, err := Analyze(t.Context(), paths, Options{Java: java, Release: release, MethodBodies: true})
	if err != nil {
		t.Fatal(err)
	}
	units, err := EmitGPPPackages(report)
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != len(sources) {
		t.Fatalf("expected four logical packages, got %d", len(units))
	}
	var inputs []string
	for _, unit := range units {
		path := filepath.Join(root, unit.Name+".gpp")
		writeTestFile(t, path, unit.Source)
		inputs = append(inputs, path)
	}
	output := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions(inputs, output,
		compiler.CompileOptions{ModulePath: "example.com/java-packages"}); err != nil {
		for _, unit := range units {
			t.Logf("%s\n%s", unit.Name, unit.Source)
		}
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(output, "main.go"), `package main
import (
    "fmt"
    "example.com/java-packages/reference/consumer"
    extension "example.com/java-packages/reference/extension/shared"
    foundation "example.com/java-packages/reference/foundation/shared"
    "example.com/java-packages/reference/plugins"
)
func main() {
    facade := consumer.NewFacade()
    child := plugins.NewChildWithInt(19)
    var base *foundation.Base = child.GppAsBase()
    var middle *extension.Middle = child.GppAsMiddle()
    if middle.GppAsBase() != base || (*plugins.Child)(nil).GppAsBase() != nil {
        panic("base identity across package boundaries")
    }
    fmt.Println(base.Seen(), base.Observed(), base.Seed())
    fmt.Println(base.Read(), middle.Score(), child.MiddleRead(), child.BaseRead())
    fmt.Println(facade.Create(13).Read(), facade.Create(23).Seed())
    fmt.Println(facade.Compare(middle, base), facade.Same(base, child), facade.HandleNull(nil))
}
`)
	got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", ".")
	if got != want {
		t.Fatalf("Go and JDK cross-package behavior diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}

func TestJavaBatchDiagnosesGoImportCycles(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	a := filepath.Join(root, "A.java")
	b := filepath.Join(root, "B.java")
	writeTestFile(t, a, "package reference.a; public class A { private reference.b.B other; }")
	writeTestFile(t, b, "package reference.b; public class B { private reference.a.A other; }")
	report, err := Analyze(t.Context(), []string{a, b}, Options{Java: java, Release: release, MethodBodies: true})
	if err != nil {
		t.Fatal(err)
	}
	units, err := EmitGPPPackages(report)
	if units != nil || err == nil || !strings.Contains(err.Error(), "[reference.a, reference.b]") {
		t.Fatalf("expected a diagnostic naming the Go import cycle, got %+v with %v", units, err)
	}
}
