package javaport

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestJavaObjectConstructionMatchesReference(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	sources := map[string]string{
		"Base.java": `package reference.objects;
public class Base {
    private final int before = read();
    private final long configured;
    protected Base(int ignored) { this(99L); }
    protected Base(long configured) { this.configured = configured; }
    public int read() { return 100; }
    public int observed() { return before; }
    public long configured() { return configured; }
}`,
		"Middle.java": `package reference.objects;
public class Middle extends Base {
    protected Middle(int configured) { super(configured); }
    @Override public int read() { return 50; }
    public int baseRead() { return super.read(); }
}`,
		"Child.java": `package reference.objects;
public final class Child extends Middle {
    private int setting = 12;
    private final int initialized;
    { initialized = setting; setting = 13; }
    private final int blockResult = setting;
    public Child() { this(123); }
    public Child(int setting) { super(setting); this.setting = setting; }
    @Override public int read() { return setting; }
    public int getInitialized() { return initialized; }
    public int getBlockResult() { return blockResult; }
    public int middleRead() { return super.read(); }
    public byte narrow() { return (byte)(1000); }
}`,
		"Marker.java": `package reference.objects;
public final class Marker {
    public static final int MAGIC = 7;
}`,
	}
	packageDirectory := filepath.Join(root, "reference", "objects")
	if err := os.MkdirAll(packageDirectory, 0755); err != nil {
		t.Fatal(err)
	}
	var sourcePaths []string
	for name, source := range sources {
		path := filepath.Join(packageDirectory, name)
		writeTestFile(t, path, source)
		sourcePaths = append(sourcePaths, path)
	}
	probe := filepath.Join(root, "Probe.java")
	writeTestFile(t, probe, `import reference.objects.*;
class Probe {
    public static void main(String[] args) {
        var child = new Child();
        System.out.println(child.observed() + " " + child.configured() + " " + child.read());
        System.out.println(child.getInitialized() + " " + child.getBlockResult());
        System.out.println(child.middleRead() + " " + child.baseRead());
        Base asBase = child;
        System.out.println(asBase.read() + " " + new Child(43).read() + " " + child.narrow());
        System.out.println((new Marker() != new Marker()) + " " + Marker.MAGIC);
    }
}`)
	classes := filepath.Join(root, "classes")
	compilerArgs := []string{"--release", strconv.Itoa(release), "-d", classes, probe}
	compilerArgs = append(compilerArgs, sourcePaths...)
	javac := filepath.Join(filepath.Dir(java), "javac")
	runObjectTestCommand(t, root, javac, compilerArgs...)
	want := runObjectTestCommand(t, root, java, "-cp", classes, "Probe")

	report, err := Analyze(t.Context(), sourcePaths, Options{Java: java, Release: release, MethodBodies: true})
	if err != nil {
		t.Fatal(err)
	}
	gpp, err := EmitGPP(report)
	if err != nil {
		t.Fatal(err)
	}
	gppFile := filepath.Join(root, "translated.gpp")
	writeTestFile(t, gppFile, gpp)
	generated := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions([]string{gppFile}, generated,
		compiler.CompileOptions{ModulePath: "example.com/java-objects"}); err != nil {
		t.Fatalf("compile generated GPP: %v\n%s", err, gpp)
	}
	writeTestFile(t, filepath.Join(generated, "main.go"), `package main
import (
    "fmt"
    "example.com/java-objects/reference/objects"
)
type goReader interface { Read() int32 }
func main() {
    child := objects.NewChild()
    fmt.Println(child.Observed(), child.Configured(), child.Read())
    fmt.Println(child.GetInitialized(), child.GetBlockResult())
    fmt.Println(child.MiddleRead(), child.BaseRead())
    var asReader goReader = child.GppAsBase()
    if child.GppAsBase() != child.GppAsMiddle().GppAsBase() || (*objects.Child)(nil).GppAsBase() != nil {
        panic("Go ancestor views must be stable and preserve nil")
    }
    fmt.Println(asReader.Read(), objects.NewChildWithInt(43).Read(), child.Narrow())
    fmt.Println(objects.NewMarker() != objects.NewMarker(), objects.MarkerMAGIC)
}
`)
	got := runObjectTestCommand(t, generated, "go", "run", "-buildvcs=false", ".")
	if got != want || !strings.HasPrefix(want, "0 99 123\n12 13\n50 100\n") {
		t.Fatalf("Go and JDK constructor behavior diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}

func TestJavaObjectEmissionRejectsPreSuperStatements(t *testing.T) {
	java, release := referenceJDK(t)
	if release < 25 {
		t.Skip("flexible constructor bodies are finalized in Java SE 25")
	}
	path := filepath.Join(t.TempDir(), "Example.java")
	writeTestFile(t, path, `package reference.objects;
public class Example {
    private final int value;
    public Example() {
        int evaluatedBeforeSuper = 1;
        super();
        this.value = evaluatedBeforeSuper;
    }
}`)
	report, err := Analyze(t.Context(), []string{path}, Options{Java: java, Release: release, MethodBodies: true})
	if err != nil || len(report.Diagnostics) != 0 {
		t.Fatalf("javac must accept the source: %v %+v", err, report.Diagnostics)
	}
	if output, err := EmitGPP(report); output != "" || err == nil || !strings.Contains(err.Error(), "invocation before the constructor body") {
		t.Fatalf("emission must not reorder Java constructor statements: %v\n%s", err, output)
	}
}

func runObjectTestCommand(t *testing.T, directory, command string, arguments ...string) string {
	t.Helper()
	process := exec.CommandContext(t.Context(), command, arguments...)
	process.Dir = directory
	output, err := process.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", command, err, output)
	}
	return string(output)
}
