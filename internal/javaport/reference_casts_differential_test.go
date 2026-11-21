package javaport

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestJavaReferenceCastsMatchOpenJDK(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	base := filepath.Join(root, "Base.java")
	writeTestFile(t, base, `package reference.casts.base;
import reference.casts.branch.Branch;
public class Base implements AutoCloseable {
    private final Object original = this;
    private final boolean duringConstruction;
    public Base() { duringConstruction = this instanceof Branch; }
    public Object original() { return original; }
    public Base baseView() { return this; }
    public boolean duringConstruction() { return duringConstruction; }
    public int score() { return 1; }
    @Override public void close() {}
}`)
	branch := filepath.Join(root, "Branch.java")
	writeTestFile(t, branch, `package reference.casts.branch;
import reference.casts.base.Base;
public class Branch extends Base {
    @Override public int score() { return 2; }
}`)
	leaf := filepath.Join(root, "Leaf.java")
	writeTestFile(t, leaf, `package reference.casts.leaf;
import reference.casts.branch.Branch;
public final class Leaf extends Branch {
    @Override public int score() { return 3; }
}`)
	casts := filepath.Join(root, "Casts.java")
	writeTestFile(t, casts, `package reference.casts.probe;
import reference.casts.base.Base;
import reference.casts.branch.Branch;
import reference.casts.leaf.Leaf;
public class Casts {
    private Base selected;
    private int calls;
    public Object widen(Base value) { return value; }
    public Base base(Object value) { return (Base) value; }
    public Leaf leaf(Base value) { return (Leaf) value; }
    public Branch branch(AutoCloseable value) { return (Branch) value; }
    public AutoCloseable closeable(Base value) { return value; }
    public boolean identity(Base value, Object other) { return value == other; }
    public boolean closeableIdentity(AutoCloseable left, AutoCloseable right) { return left == right; }
    public boolean isObject(Object value) { return value instanceof Object; }
    public boolean isBase(Object value) { return value instanceof Base; }
    public boolean isLeaf(Base value) { return value instanceof Leaf; }
    public boolean isCloseable(Object value) { return value instanceof AutoCloseable; }
    public boolean isNullInstance() { return null instanceof Branch; }
    public boolean stringCast() { return (String) (Object) "value" == "value"; }
    public boolean wrapperInstance(Object value) { return value instanceof Integer; }
    public int castThenDispatch(Object value) { return ((Branch) value).score(); }
    public int catchWrongType(Object value) {
        try { return ((Leaf) value).score(); }
        catch (ClassCastException failure) { return -13; }
        catch (NullPointerException failure) { return -17; }
    }
    private Base next() {
        calls = calls + 1;
        return selected;
    }
    public boolean singleTest(Base value) {
        selected = value;
        return next() instanceof Branch;
    }
    public Branch singleCast() { return (Branch) next(); }
    public int calls() { return calls; }
}`)
	probe := filepath.Join(root, "Probe.java")
	writeTestFile(t, probe, `import reference.casts.base.Base;
import reference.casts.branch.Branch;
import reference.casts.leaf.Leaf;
import reference.casts.probe.Casts;
class Probe {
    public static void main(String[] args) {
        var plain = new Base();
        var branch = new Branch();
        var leaf = new Leaf();
        Base view = leaf.baseView();
        var casts = new Casts();
        Object value = casts.widen(view);
        System.out.println((value == leaf) + " " + (value == view.original()) + " " +
            (casts.base(value) == view) + " " + (casts.leaf(view) == leaf) + " " +
            (casts.branch(casts.closeable(view)) == leaf) + " " +
            casts.identity(view, leaf) + " " + casts.identity(view, new Leaf()) + " " +
            casts.closeableIdentity(view, leaf));
        System.out.println(plain.duringConstruction() + " " + branch.duringConstruction() + " " +
            leaf.duringConstruction() + " " + casts.castThenDispatch(value));
        System.out.println(casts.isObject(null) + " " + casts.isObject(view) + " " + casts.isBase(leaf) + " " +
            casts.isLeaf(branch) + " " + casts.isLeaf(view) + " " + casts.isCloseable(view) + " " +
            casts.isNullInstance() + " " + casts.stringCast() + " " + casts.wrapperInstance(1));
        System.out.println((casts.widen(null) == null) + " " + (casts.base(null) == null) + " " +
            (casts.leaf(null) == null) + " " + casts.catchWrongType(branch) + " " +
            casts.catchWrongType("not a class") + " " + casts.catchWrongType(null));
        System.out.println(casts.singleTest(view) + " " + (casts.singleCast() == leaf) + " " + casts.calls());
    }
}`)
	inputs := []string{base, branch, leaf, casts}
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
	var implementations int
	for _, unit := range units {
		if strings.HasPrefix(unit.Name, "internal.gppjavaport.") {
			implementations++
		}
		file := filepath.Join(root, unit.Name+".gpp")
		writeTestFile(t, file, unit.Source)
		sources = append(sources, file)
	}
	if implementations != 1 {
		t.Fatalf("expected the Base/Branch cycle to produce one shared implementation, got %d", implementations)
	}
	output := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions(sources, output, compiler.CompileOptions{
		ModulePath: "example.com/java-reference",
	}); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(root, "external")
	if err := os.Mkdir(external, 0755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(external, "go.mod"), `module example.com/reference-consumer

go 1.27

require example.com/java-reference v0.0.0

replace example.com/java-reference => ../generated
`)
	writeTestFile(t, filepath.Join(external, "main.go"), `package main

import (
    "fmt"

    java "example.com/java-reference/gpp/java"
    "example.com/java-reference/reference/casts/base"
    "example.com/java-reference/reference/casts/branch"
    "example.com/java-reference/reference/casts/leaf"
    "example.com/java-reference/reference/casts/probe"
)

func main() {
    plain := base.NewBase()
    middle := branch.NewBranch()
    deepest := leaf.NewLeaf()
    var view *base.Base = deepest.BaseView()
    casts := probe.NewCasts()
    value := casts.Widen(view)
    fmt.Println(value == deepest, value == view.Original(), casts.Base(value) == view,
        casts.Leaf(view) == deepest, casts.Branch(casts.Closeable(view)) == deepest.GppAsBranch(),
        casts.Identity(view, deepest), casts.Identity(view, leaf.NewLeaf()),
        casts.CloseableIdentity(view, deepest))
    fmt.Println(plain.DuringConstruction(), middle.DuringConstruction(),
        deepest.DuringConstruction(), casts.CastThenDispatch(value))
    fmt.Println(casts.IsObject(nil), casts.IsObject(view), casts.IsBase(deepest),
        casts.IsLeaf(middle.GppAsBase()), casts.IsLeaf(view), casts.IsCloseable(view),
        casts.IsNullInstance(), casts.StringCast(), casts.WrapperInstance(java.BoxInteger(1)))
    fmt.Println(casts.Widen(nil) == nil, casts.Base(nil) == nil, casts.Leaf(nil) == nil,
        casts.CatchWrongType(middle), casts.CatchWrongType(java.StringFromGo("not a class")),
        casts.CatchWrongType((*branch.Branch)(nil)))
    fmt.Println(casts.SingleTest(view), casts.SingleCast() == deepest.GppAsBranch(), casts.Calls())
}
`)
	got := runObjectTestCommand(t, external, "go", "run", "-buildvcs=false", ".")
	if want != got {
		t.Fatalf("Go and OpenJDK reference semantics diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}
