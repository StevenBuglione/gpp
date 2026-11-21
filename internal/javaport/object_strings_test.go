package javaport

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestJavaStringsMatchReference(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	source := filepath.Join(root, "Text.java")
	writeTestFile(t, source, `package reference.text;
public class Text {
    public static final String EMPTY = "";
    private final String literal = "A\uD834\uDD1E\uD800";
    private final String value;

    public Text() { this(null); }
    public Text(String value) { this.value = value; }
    public String getValue() { return value; }
    public String getLiteral() { return literal; }
    public String readAgain() { return "A\uD834\uDD1E\uD800"; }
    public String copyValue() { return new String(value); }
    public boolean valueIsLiteral() { return value == literal; }
    public boolean valueMatchesLiteral() { return value.equals(literal); }
    public boolean matchesNull() { return literal.equals(null); }
    public boolean isNull() { return value == null; }
    public boolean hasDistinctEmpty() { return new String() != EMPTY; }
    public String internValue() { return new String(value).intern(); }
    public int getLength() { return value.length(); }
    public int getCodeUnit(int index) { return value.charAt(index); }
    public int getHash() { return value.hashCode(); }
}`)
	otherPackage := filepath.Join(root, "reference", "other")
	if err := os.MkdirAll(otherPackage, 0755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(otherPackage, "Other.java")
	writeTestFile(t, other, `package reference.other;
public class Other {
    public String literal() { return "A\uD834\uDD1E\uD800"; }
}`)
	probe := filepath.Join(root, "Probe.java")
	writeTestFile(t, probe, `package reference.text;
class Probe {
    public static void main(String[] args) {
        var literals = new Text();
        var javaLiteral = literals.getLiteral();
        var fresh = new String(javaLiteral);
        var sample = new Text(fresh);
        System.out.printf("%b %b %b %b %b %b %b%n",
            new Text().isNull(), literals.readAgain() == javaLiteral, sample.getValue() == fresh,
            sample.valueIsLiteral(), sample.valueMatchesLiteral(), sample.hasDistinctEmpty(), sample.matchesNull());
        System.out.printf("%b %b%n", sample.copyValue() == fresh, sample.internValue() == javaLiteral);
        System.out.println(new reference.other.Other().literal() == javaLiteral);
        System.out.printf("%d %04x %04x %04x %04x %d%n", sample.getLength(),
            sample.getCodeUnit(0), sample.getCodeUnit(1), sample.getCodeUnit(2), sample.getCodeUnit(3), sample.getHash());
        reportFailure(sample, 4);
        reportFailure(literals, 0);
    }
    private static void reportFailure(Text text, int offset) {
        try { text.getCodeUnit(offset); }
        catch (RuntimeException error) { System.out.println(error.getClass().getName()); }
    }
}`)
	classes := filepath.Join(root, "classes")
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"),
		"--release", strconv.Itoa(release), "-d", classes, source, other, probe)
	want := runObjectTestCommand(t, root, java, "-cp", classes, "reference.text.Probe")

	var gppFiles []string
	for _, path := range []string{source, other} {
		report, err := Analyze(t.Context(), []string{path}, Options{Java: java, Release: release, MethodBodies: true})
		if err != nil {
			t.Fatal(err)
		}
		translated, err := EmitGPP(report)
		if err != nil {
			t.Fatal(err)
		}
		gpp := path + ".gpp"
		writeTestFile(t, gpp, translated)
		gppFiles = append(gppFiles, gpp)
	}
	observer := filepath.Join(root, "observer.gpp")
	writeTestFile(t, observer, `package reference.observer
import (
    "gpp/java"
    "example.com/java-strings/reference/text"
)

func CodeUnitFromGo(value *text.Text, index int32) (int32, error) {
    try {
        return value.GetCodeUnit(index), nil
    } catch java.RuntimeException failure {
        return int32(0), failure
    }
}

func CodeUnitWithCleanup(value *text.Text, index int32, cleanup func()) (int32, error) {
    try {
        return value.GetCodeUnit(index), nil
    } finally {
        cleanup()
    }
}

func readCodeUnit(value *text.Text, index int32) int32 {
    return value.GetCodeUnit(index)
}

func CodeUnitOrFallback(value *text.Text, index int32) int32 {
    return readCodeUnit(value, index) ?? -1
}

func RethrowCallback(callback func()) {
    try {
        callback()
    } catch java.RuntimeException failure {
        throw failure
    }
}

func ForwardCallback(callback func()) error {
    try {
        RethrowCallback(callback)
    } catch java.Throwable failure {
        return failure
    }
    return nil
}
`)
	output := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions(gppFiles, output,
		compiler.CompileOptions{ModulePath: "example.com/java-strings"}); err != nil {
		t.Fatalf("compile translated strings: %v", err)
	}
	observerOutput := filepath.Join(root, "observer")
	if err := compiler.CompileFilesWithOptions([]string{observer}, observerOutput, compiler.CompileOptions{
		ModulePath: "example.com/java-observer", JavaRuntimeImport: "example.com/java-strings/gpp/java",
	}); err != nil {
		t.Fatalf("compile observer against the shared Java exception runtime: %v", err)
	}
	consumer := filepath.Join(root, "consumer")
	if err := os.Mkdir(consumer, 0755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(consumer, "go.mod"), "module example.com/java-consumer\n\ngo 1.27\n")
	workspace := filepath.Join(root, "go.work")
	writeTestFile(t, workspace, "go 1.27\n\nuse (\n\t./generated\n\t./observer\n\t./consumer\n)\n")
	t.Setenv("GOWORK", workspace)
	t.Setenv("GOPROXY", "off")
	writeTestFile(t, filepath.Join(consumer, "main.go"), `package main
import (
    "errors"
    "fmt"
    "slices"
    "example.com/java-observer/reference/observer"
    "example.com/java-strings/gpp/java"
    "example.com/java-strings/reference/other"
    "example.com/java-strings/reference/text"
)
func main() {
    literals := text.NewText()
    javaLiteral := literals.GetLiteral()
    units := []uint16{0x0041, 0xd834, 0xdd1e, 0xd800}
    fresh := java.StringFromUTF16(units)
    units[0] = 0
    returnedUnits := fresh.UTF16()
    returnedUnits[0] = 0
    if !slices.Equal(fresh.UTF16(), javaLiteral.UTF16()) { panic("String must copy code units") }
    if !slices.Equal(java.StringFromGo("A𝄞").UTF16(), []uint16{0x0041, 0xd834, 0xdd1e}) { panic("invalid UTF-16 bridge result") }
    if decoded, ok := javaLiteral.ToGo(); !ok || decoded != "A𝄞�" { panic("invalid UTF-8 bridge result") }
    if decoded, ok := (*java.String)(nil).ToGo(); ok || decoded != "" { panic("lost null") }
    if decoded, ok := text.TextEMPTY().ToGo(); !ok || decoded != "" { panic("lost empty String") }
    sample := text.NewTextWithString(fresh)
    fmt.Printf("%t %t %t %t %t %t %t\n", text.NewText().IsNull(), literals.ReadAgain() == javaLiteral,
        sample.GetValue() == fresh, sample.ValueIsLiteral(), sample.ValueMatchesLiteral(), sample.HasDistinctEmpty(), sample.MatchesNull())
    fmt.Printf("%t %t\n", sample.CopyValue() == fresh, sample.InternValue() == javaLiteral)
    fmt.Println(other.NewOther().Literal() == javaLiteral)
    fmt.Printf("%d %04x %04x %04x %04x %d\n", sample.GetLength(), sample.GetCodeUnit(0),
        sample.GetCodeUnit(1), sample.GetCodeUnit(2), sample.GetCodeUnit(3), sample.GetHash())
    reportFailure(sample, 4)
    reportFailure(literals, 0)

    cleanups := 0
    _, err := observer.CodeUnitWithCleanup(literals, 0, func() { cleanups++ })
    var nullFailure *java.NullPointerException
    if cleanups != 1 || !errors.As(err, &nullFailure) { panic("lost cleanup or original error type at the Go boundary") }
    if observer.CodeUnitOrFallback(sample, 0) != 65 || observer.CodeUnitOrFallback(literals, 0) != -1 {
        panic("cross-module String errors did not reach coalescing")
    }
    original := &java.IllegalArgumentException{Message: "round trip"}
    if err := observer.ForwardCallback(func() { java.Raise(original) }); err != original { panic("rethrow replaced the original exception") }
    native := disguisedPanic{original}
    if recovered := recoverNative(native); recovered != native { panic("ordinary Go panic was converted into an exception") }
}
func reportFailure(value *text.Text, index int32) {
    _, err := observer.CodeUnitFromGo(value, index)
    var javaError java.RuntimeException
    if !errors.As(err, &javaError) { panic(fmt.Sprintf("expected typed exception, got %v", err)) }
    fmt.Println(javaError.JavaClassName())
}
type disguisedPanic struct { error }
func (value disguisedPanic) GppThrownError() error { return value.error }
func recoverNative(value any) (recovered any) {
    defer func() { recovered = recover() }()
    _ = observer.ForwardCallback(func() { panic(value) })
    return nil
}
`)
	got := runObjectTestCommand(t, consumer, "go", "run", "-buildvcs=false", ".")
	if got != want || !strings.Contains(want, "4 0041 d834 dd1e d800") {
		t.Fatalf("Go and JDK String behavior diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}
