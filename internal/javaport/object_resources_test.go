package javaport

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestJavaTryWithResourcesMatchesReference(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	trace := filepath.Join(root, "Trace.java")
	writeTestFile(t, trace, `package reference.resources;
public class Trace {
    private int digits;
    public void record(int digit) { digits = digits * 10 + digit; }
    public int value() { return digits; }
}`)
	resource := filepath.Join(root, "Resource.java")
	writeTestFile(t, resource, `package reference.resources;
import java.io.IOException;
public class Resource implements AutoCloseable {
    private Trace trace;
    private int closeDigit;
    private IOException closeFailure;
    public Resource(Trace trace, int openDigit, int closeDigit, IOException initFailure, IOException closeFailure) throws IOException {
        this.trace = trace;
        this.closeDigit = closeDigit;
        this.closeFailure = closeFailure;
        trace.record(openDigit);
        if (initFailure != null) { throw initFailure; }
    }
    @Override public void close() throws IOException {
        trace.record(closeDigit);
        if (closeFailure != null) { throw closeFailure; }
    }
    public int digit() { return closeDigit; }
}`)
	use := filepath.Join(root, "UseResources.java")
	writeTestFile(t, use, `package reference.resources;
import java.io.IOException;
public class UseResources {
    public int run(Trace trace, IOException init, IOException body, IOException firstClose, IOException secondClose) throws IOException {
        try (var first = new Resource(trace, 1, 6, null, firstClose);
             var second = new Resource(trace, first.digit(), 5, init, secondClose)) {
            trace.record(3);
            if (body != null) { throw body; }
            return second.digit();
        } finally { trace.record(7); }
    }
    public int capture(Trace trace, IOException firstClose, IOException secondClose) {
        try { return run(trace, null, new IOException("body"), firstClose, secondClose); }
        catch (IOException failure) { return failure.getSuppressed().length; }
    }
    public int existing(Trace trace, AutoCloseable outside) throws Exception {
        var another = new Resource(trace, 1, 6, null, null);
        try (another; outside) { return another.digit(); }
    }
}`)
	probe := filepath.Join(root, "Probe.java")
	writeTestFile(t, probe, `package reference.resources;
import java.io.IOException;
class Probe {
    public static void main(String[] args) throws Exception {
        var use = new UseResources();
        var first = new IOException("first");
        var second = new IOException("second");
        var body = new IOException("body");
        var init = new IOException("initialization");
        run(use, null, null, null, null, null);
        run(use, null, body, first, second, body, second, first);
        run(use, init, null, first, second, init, first);
        run(use, null, null, first, second, second, first);
        Throwable self = run(use, null, body, first, body, null, first);
        System.out.println(self.getClass().getName());
        var trace = new Trace();
        System.out.println(use.capture(trace, first, second) + " " + trace.value());
        trace = new Trace();
        System.out.println(use.existing(trace, null) + " " + trace.value());
        final var callbackTrace = new Trace();
        System.out.println(use.existing(callbackTrace, () -> callbackTrace.record(9)) + " " + callbackTrace.value());
    }
    private static Throwable run(UseResources use, IOException init, IOException body, IOException first,
            IOException second, Throwable expected, Throwable... suppressed) {
        var trace = new Trace();
        try { System.out.println(use.run(trace, init, body, first, second) + " " + trace.value()); }
        catch (Exception failure) {
            if (expected != null && failure != expected) { throw new AssertionError("primary identity"); }
            var actual = failure.getSuppressed();
            if (!java.util.Arrays.equals(actual, suppressed)) { throw new AssertionError("suppressed identity/order"); }
            if (actual.length > 0) {
                actual[0] = null;
                if (failure.getSuppressed()[0] != suppressed[0]) { throw new AssertionError("defensive copy"); }
            }
            System.out.println(failure.getClass().getName() + " " + suppressed.length + " " + trace.value());
            return failure;
        }
        if (expected != null || suppressed.length > 0) { throw new AssertionError("missing exception"); }
        return null;
    }
}`)
	sources := []string{trace, resource, use}
	classes := filepath.Join(root, "classes")
	javacArgs := []string{"--release", strconv.Itoa(release), "-d", classes, trace, resource, use, probe}
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"), javacArgs...)
	want := runObjectTestCommand(t, root, java, "-cp", classes, "reference.resources.Probe")
	report, err := Analyze(t.Context(), sources, Options{Java: java, Release: release, MethodBodies: true})
	if err != nil {
		t.Fatal(err)
	}
	translated, err := EmitGPP(report)
	if err != nil {
		t.Fatal(err)
	}
	gpp := filepath.Join(root, "resources.gpp")
	writeTestFile(t, gpp, translated)
	output := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions([]string{gpp}, output,
		compiler.CompileOptions{ModulePath: "example.com/java-resources"}); err != nil {
		t.Fatalf("compile translated resources: %v\n%s", err, translated)
	}
	writeTestFile(t, filepath.Join(output, "main.go"), `package main
import (
    "errors"
    "fmt"
    "slices"
    "example.com/java-resources/gpp/java"
    "example.com/java-resources/reference/resources"
)
type nativeResource struct { trace *resources.Trace; failure *java.IOException }

func (*nativeResource) GppAutoCloseable() {}
func (resource *nativeResource) Close() {
    resource.trace.Record(9)
    if resource.failure != nil { java.Raise(resource.failure) }
}
func main() {
    use := resources.NewUseResources()
    first := java.NewIOException(java.StringFromGo("first"))
    second := java.NewIOException(java.StringFromGo("second"))
    body := java.NewIOException(java.StringFromGo("body"))
    init := java.NewIOException(java.StringFromGo("initialization"))
    run(use, nil, nil, nil, nil, nil)
    run(use, nil, body, first, second, body, second, first)
    run(use, init, nil, first, second, init, first)
    run(use, nil, nil, first, second, second, first)
    self := run(use, nil, body, first, body, nil, first)
    fmt.Println(self.JavaClassName())
    trace := resources.NewTrace()
    fmt.Println(use.Capture(trace, first, second), trace.Value())
    trace = resources.NewTrace()
    var typedNil *resources.Resource
    value, err := use.GppCallExisting(trace, typedNil)
    if err != nil { panic(err) }
    fmt.Println(value, trace.Value())
    trace = resources.NewTrace()
    value, err = use.GppCallExisting(trace, &nativeResource{trace: trace})
    if err != nil { panic(err) }
    fmt.Println(value, trace.Value())
    trace = resources.NewTrace()
    _, err = use.GppCallExisting(trace, &nativeResource{trace: trace, failure: init})
    if err != init || trace.Value() != 196 { panic("Go resource failure did not round trip") }
    literal := &java.IllegalArgumentException{Message: "literal"}
    java.AddSuppressed(literal, init)
    if !slices.Equal(java.Suppressed(literal), []java.Throwable{init}) { panic("Go literal lost suppressed exception") }
}
func run(use *resources.UseResources, init, body, first, second *java.IOException,
        expected java.Throwable, suppressed ...java.Throwable) java.Throwable {
    trace := resources.NewTrace()
    value, err := use.GppCallRun(trace, init, body, first, second)
    if err == nil {
        if expected != nil || len(suppressed) != 0 { panic("missing exception") }
        fmt.Println(value, trace.Value())
        return nil
    }
    var failure java.Throwable
    if !errors.As(err, &failure) || (expected != nil && failure != expected) { panic("primary identity") }
    actual := java.Suppressed(failure)
    if !slices.Equal(actual, suppressed) { panic("suppressed identity/order") }
    if len(actual) > 0 {
        actual[0] = nil
        if java.Suppressed(failure)[0] != suppressed[0] { panic("defensive copy") }
    }
    fmt.Println(failure.JavaClassName(), len(suppressed), trace.Value())
    return failure
}
`)
	got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", ".")
	if got != want {
		t.Fatalf("Go and JDK resource handling diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}
