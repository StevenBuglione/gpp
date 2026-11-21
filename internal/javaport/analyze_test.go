package javaport

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestJavaSources(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "Example.java")
	writeTestFile(t, source, "class Example {}")
	writeTestFile(t, filepath.Join(root, "README.md"), "Not input.")
	got, err := javaSources([]string{root, source})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{source}) {
		t.Fatalf("sources = %v, want [%s]", got, source)
	}
}

func TestAnalyzeResolvesOverloadsAndSourceRanges(t *testing.T) {
	java, release := referenceJDK(t)
	path := filepath.Join(t.TempDir(), "example source.java")
	source := `package example;
import java.util.function.Function;
class Example {
    static String pick(Object value) { return "object"; }
    static String pick(String value) { return value; }
    String call() { return pick("é😀"); }
    Function<String, String> reference() { return Example::pick; }
}
`
	writeTestFile(t, path, source)

	report, err := Analyze(t.Context(), []string{path}, Options{Java: java, Release: release})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Diagnostics) != 0 || report.Release != release || len(report.Files) != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
	file := report.Files[0]
	if file.Package != "example" || file.Path != path || len(file.Declarations) != 5 {
		t.Fatalf("unexpected source inventory: %+v", file)
	}
	if len(file.Calls) != 2 {
		t.Fatalf("calls = %+v, want one invocation and one method reference", file.Calls)
	}
	call, reference := file.Calls[0], file.Calls[1]
	for _, occurrence := range file.Calls {
		if occurrence.Symbol != "example.Example#pick(java.lang.String)" {
			t.Fatalf("wrong overload: %+v", occurrence)
		}
	}
	const callSource = `pick("é😀")`
	start := strings.Index(source, callSource)
	end := len(utf16.Encode([]rune(source[:start+len(callSource)])))
	if call.Kind != "METHOD_INVOCATION" || call.Type != "java.lang.String" ||
		call.Start != int64(start) || call.End != int64(end) || call.Line != 6 {
		t.Fatalf("unexpected call location or type: %+v", call)
	}
	if reference.Kind != "MEMBER_REFERENCE" || reference.Type != "java.util.function.Function<java.lang.String,java.lang.String>" {
		t.Fatalf("unexpected reference: %+v", reference)
	}
}

func TestAnalyzeReturnsCompilerDiagnostics(t *testing.T) {
	java, release := referenceJDK(t)
	path := filepath.Join(t.TempDir(), "Broken.java")
	writeTestFile(t, path, "class Broken { void method() { missing(); } }")

	report, err := Analyze(t.Context(), []string{path}, Options{Java: java, Release: release})
	if err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Kind == "ERROR" && diagnostic.Path == path && diagnostic.Line == 1 && diagnostic.Code != "" {
			return
		}
	}
	t.Fatalf("no source diagnostic: %+v", report.Diagnostics)
}

func referenceJDK(t *testing.T) (string, int) {
	t.Helper()
	java := os.Getenv("GPP_TEST_JAVA")
	if java == "" {
		t.Skip("set GPP_TEST_JAVA to test with a reference JDK")
	}
	release := 17
	if configured := os.Getenv("GPP_TEST_JAVA_RELEASE"); configured != "" {
		var err error
		release, err = strconv.Atoi(configured)
		if err != nil {
			t.Fatal(err)
		}
	}
	return java, release
}

func writeTestFile(t *testing.T, path, source string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestAnalyzeJava27Preview(t *testing.T) {
	java, release := referenceJDK(t)
	if release != 27 {
		t.Skip("requires the Java 27 reference JDK")
	}
	path := filepath.Join(t.TempDir(), "Preview.java")
	writeTestFile(t, path, `class Preview {
    boolean exactlyRepresentable(double value) {
        return value instanceof int integer;
    }
}
`)

	for _, enabled := range []bool{false, true} {
		report, err := Analyze(t.Context(), []string{path}, Options{Java: java, Release: release, Preview: enabled})
		if err != nil {
			t.Fatal(err)
		}
		hasError := slices.ContainsFunc(report.Diagnostics, func(diagnostic Diagnostic) bool {
			return diagnostic.Kind == "ERROR"
		})
		if report.Preview != enabled || hasError == enabled {
			t.Fatalf("preview=%v: unexpected report %+v", enabled, report)
		}
	}
}

func TestEmitPrimitiveInterface(t *testing.T) {
	java, release := referenceJDK(t)
	path := filepath.Join(t.TempDir(), "Ordered.java")
	writeTestFile(t, path, `package example.core;
public interface Ordered {
    int FIRST = Integer.MIN_VALUE;
    int order();
    char echo(char value);
}
`)
	report, err := Analyze(t.Context(), []string{path}, Options{Java: java, Release: release})
	if err != nil {
		t.Fatal(err)
	}
	got, err := EmitInterfaces(report)
	if err != nil {
		t.Fatal(err)
	}
	want := "package example.core\n\ntype Ordered interface {\n" +
		"\tOrder() int32\n\tEcho(uint16) uint16\n}\n\nconst OrderedFIRST int32 = -2147483648\n"
	if got != want {
		t.Fatalf("emitted GPP:\n%s\nwant:\n%s", got, want)
	}

	writeTestFile(t, path, `package example.core;
public interface Ordered {
    default int order() { return 0; }
}
`)
	report, err = Analyze(t.Context(), []string{path}, Options{Java: java, Release: release})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := EmitInterfaces(report); err == nil || got != "" {
		t.Fatalf("emitted an unsupported default method: %q, %v", got, err)
	}
}

func TestEmitInheritedInterfaceWithMarker(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "Ordered.java"),
		"package example; public interface Ordered { int order(); }")
	writeTestFile(t, filepath.Join(root, "PriorityOrdered.java"),
		"package example; public interface PriorityOrdered extends Ordered {}")
	report, err := Analyze(t.Context(), []string{root}, Options{Java: java, Release: release})
	if err != nil {
		t.Fatal(err)
	}
	got, err := EmitInterfaces(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "type PriorityOrdered interface {\n\tOrdered\n\tIsPriorityOrdered()\n}") {
		t.Fatalf("missing inherited nominal interface:\n%s", got)
	}
}
