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

func TestJavaPackageCyclesMatchReference(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	for _, directory := range []string{"left", "right", "consumer", "input", "external"} {
		if err := os.Mkdir(filepath.Join(root, directory), 0755); err != nil {
			t.Fatal(err)
		}
	}
	left := filepath.Join(root, "left", "Node.java")
	right := filepath.Join(root, "right", "Node.java")
	parent := filepath.Join(root, "left", "Parent.java")
	facade := filepath.Join(root, "consumer", "Facade.java")
	writeTestFile(t, left, `package reference.left;
import java.io.IOException;
public class Node {
    public static final int KIND = 7;
    public static final String LABEL = "node";
    private reference.right.Node peer;
    public Node(reference.right.Node peer) { this.peer = peer; }
    public Node(reference.right.Node peer, boolean fail) throws IOException {
        this(peer);
        if (fail) { throw new IOException(LABEL); }
    }
    public reference.right.Node peer() { return peer; }
    public int mirror() { return peer.score(); }
}`)
	writeTestFile(t, parent, `package reference.left;
public class Parent {
    private final int during = score();
    protected Parent() {}
    public int during() { return during; }
    public int score() { return 100; }
}`)
	writeTestFile(t, right, `package reference.right;
import reference.left.Parent;
public class Node extends Parent {
    private int setting = 41;
    private reference.left.Node peer;
    public Node() {}
    public void attach(reference.left.Node peer) { this.peer = peer; }
    public reference.left.Node peer() { return peer; }
    public reference.left.Node spawn(Node other) { return new reference.left.Node(other); }
    @Override public int score() { return setting; }
    public int parentScore() { return super.score(); }
}`)
	writeTestFile(t, facade, `package reference.consumer;
public class Facade {
    public reference.left.Node connect(reference.right.Node node) { return node.spawn(node); }
}`)
	probe := filepath.Join(root, "Probe.java")
	writeTestFile(t, probe, `import java.io.IOException;
class Probe {
    public static void main(String[] args) throws IOException {
        var right = new reference.right.Node();
        var left = new reference.consumer.Facade().connect(right);
        right.attach(left);
        reference.left.Parent ancestor = right;
        System.out.println((left.peer() == right) + " " + (right.peer() == left));
        System.out.println(left.mirror() + " " + ancestor.during() + " " + ancestor.score() + " " + right.parentScore());
        System.out.println(reference.left.Node.KIND + " " + reference.left.Node.LABEL);
        try { new reference.left.Node(right, true); }
        catch (IOException failure) { System.out.println(failure.getMessage() == reference.left.Node.LABEL); }
    }
}`)
	paths := []string{left, right, parent, facade}
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
	var inputs []string
	var implementations int
	for _, unit := range units {
		if strings.HasPrefix(unit.Name, "internal.gppjavaport.") {
			implementations++
		}
		path := filepath.Join(root, "input", unit.Name+".gpp")
		writeTestFile(t, path, unit.Source)
		inputs = append(inputs, path)
	}
	if implementations != 1 || len(units) != 4 {
		t.Fatalf("expected one shared implementation and three public packages, got %+v", units)
	}
	generated := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions(inputs, generated,
		compiler.CompileOptions{ModulePath: "example.com/java-cycles"}); err != nil {
		for _, unit := range units {
			t.Logf("%s\n%s", unit.Name, unit.Source)
		}
		t.Fatal(err)
	}
	consumer := filepath.Join(root, "external")
	writeTestFile(t, filepath.Join(consumer, "go.mod"), `module example.com/consumer

go 1.27

require example.com/java-cycles v0.0.0

replace example.com/java-cycles => ../generated
`)
	writeTestFile(t, filepath.Join(consumer, "main.go"), `package main
import (
    "errors"
    "fmt"
    "example.com/java-cycles/gpp/java"
    "example.com/java-cycles/reference/consumer"
    "example.com/java-cycles/reference/left"
    "example.com/java-cycles/reference/right"
)
func main() {
    r := right.NewNode()
    var l *left.Node = consumer.NewFacade().Connect(r)
    r.Attach(l)
    var ancestor *left.Parent = r.GppAsParent()
    fmt.Println(l.Peer() == r, r.Peer() == l)
    fmt.Println(l.Mirror(), ancestor.During(), ancestor.Score(), r.ParentScore())
    label, _ := left.NodeLABEL().ToGo()
    fmt.Println(left.NodeKIND, label)
    if direct := left.NewNodeWithNode(r); direct.Peer() != r { panic("constructor lost the peer") }
    if direct, err := left.GppNewNodeWithNodeAndBoolean(r, false); err != nil || direct.Peer() != r { panic("adapter lost the peer") }
    instance, err := left.GppNewNodeWithNodeAndBoolean(r, true)
    var failure *java.IOException
    if instance != nil || !errors.As(err, &failure) { panic("missing translated constructor exception") }
    fmt.Println(failure.GetMessage() == left.NodeLABEL())
}
`)
	got := runObjectTestCommand(t, consumer, "go", "run", "-buildvcs=false", ".")
	if got != want {
		t.Fatalf("Go and JDK cyclic package behavior diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}
