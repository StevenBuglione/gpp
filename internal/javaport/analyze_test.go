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

func TestAnalyzeAnnotationDefaultsAndRetention(t *testing.T) {
	java, release := referenceJDK(t)
	path := filepath.Join(t.TempDir(), "Example.java")
	writeTestFile(t, path, `package example;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
@interface Nested { String value(); }
enum Mode { LOW, HIGH }
@Retention(RetentionPolicy.RUNTIME)
@interface Trace {
    Class<?> type() default String.class;
    int[] levels() default {1, 2};
    Nested nested() default @Nested("inside");
    char letter() default 'A';
    Mode mode() default Mode.LOW;
}
@Trace(levels = {3, 4}, mode = Mode.HIGH)
class Example {}
`)
	report, err := Analyze(t.Context(), []string{path}, Options{Java: java, Release: release})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Diagnostics) != 0 || len(report.Files) != 1 {
		t.Fatalf("unexpected annotation report: %+v", report)
	}
	declarations := report.Files[0].Declarations
	index := slices.IndexFunc(declarations, func(declaration Occurrence) bool {
		return declaration.Symbol == "example.Example"
	})
	if index < 0 || len(declarations[index].Annotations) != 1 {
		t.Fatalf("missing declaration annotation: %+v", declarations)
	}
	annotation := declarations[index].Annotations[0]
	if annotation.Type != "example.Trace" || annotation.Retention != "RUNTIME" {
		t.Fatalf("unexpected annotation identity: %+v", annotation)
	}
	members := map[string]AnnotationPair{}
	for _, member := range annotation.Members {
		members[member.Name] = member
	}
	if value := members["type"]; value.Explicit || value.Value.Kind != "CLASS_LITERAL" ||
		value.Value.Text != "java.lang.String" {
		t.Fatalf("lost default class literal: %+v", value)
	}
	if value := members["levels"]; !value.Explicit || value.Value.Kind != "ARRAY" ||
		len(value.Value.Elements) != 2 || value.Value.Elements[0].Text != "3" {
		t.Fatalf("lost explicit array: %+v", value)
	}
	if value := members["nested"].Value; value.Annotation == nil ||
		value.Annotation.Retention != "CLASS" || len(value.Annotation.Members) != 1 ||
		value.Annotation.Members[0].Value.Text != "inside" {
		t.Fatalf("lost nested annotation: %+v", value)
	}
	if value := members["letter"].Value; value.Kind != "CHAR" || value.Text != "65" {
		t.Fatalf("lost character constant: %+v", value)
	}
	if value := members["mode"]; !value.Explicit || value.Value.Text != "example.Mode#HIGH" {
		t.Fatalf("lost enum constant: %+v", value)
	}
	index = slices.IndexFunc(declarations, func(declaration Occurrence) bool {
		return declaration.Symbol == "example.Trace#type()"
	})
	if index < 0 || declarations[index].AnnotationDefault == nil ||
		declarations[index].AnnotationDefault.Text != "java.lang.String" {
		t.Fatalf("lost declaration default: %+v", declarations)
	}
}

func TestAnalyzeModuleDeclarations(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "example", "api"), 0755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "module-info.java"), `module example.port {
    requires static transitive java.logging;
    exports example.api to java.logging;
    opens example.api;
    uses example.api.Service;
    provides example.api.Service with example.api.Provider;
}
`)
	writeTestFile(t, filepath.Join(root, "example", "api", "Service.java"),
		"package example.api; public interface Service {}")
	writeTestFile(t, filepath.Join(root, "example", "api", "Provider.java"),
		"package example.api; public class Provider implements Service {}")

	report, err := Analyze(t.Context(), []string{root}, Options{Java: java, Release: release})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Diagnostics) != 0 || len(report.Files) != 3 {
		t.Fatalf("unexpected module report: %+v", report)
	}
	var module *ModuleDescriptor
	for _, source := range report.Files {
		if source.Module != nil {
			module = source.Module
		}
		for _, declaration := range source.Declarations {
			if declaration.ModuleName != "example.port" {
				t.Fatalf("lost declaring module: %+v", declaration)
			}
		}
	}
	if module == nil || module.Name != "example.port" || module.Open || len(module.Directives) != 5 {
		t.Fatalf("unexpected module: %+v", module)
	}
	directives := map[string]ModuleDirective{}
	for _, directive := range module.Directives {
		directives[directive.Kind] = directive
	}
	if require := directives["REQUIRES"]; !require.Static || !require.Transitive || require.Name != "java.logging" {
		t.Fatalf("lost requires modifiers: %+v", require)
	}
	if export := directives["EXPORTS"]; export.Name != "example.api" ||
		!slices.Equal(export.Targets, []string{"java.logging"}) {
		t.Fatalf("lost qualified export: %+v", export)
	}
	if provider := directives["PROVIDES"]; provider.Name != "example.api.Service" ||
		!slices.Equal(provider.Targets, []string{"example.api.Provider"}) {
		t.Fatalf("lost provider: %+v", provider)
	}
	if directives["OPENS"].Name != "example.api" || directives["USES"].Name != "example.api.Service" {
		t.Fatalf("lost opens or uses: %+v", directives)
	}
}

func TestReadBuildInputsResolvesRelativePaths(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "inputs.json")
	writeTestFile(t, path, `{"schemaVersion":1,"release":17,"classPath":["lib/dependency.jar"],"sources":["Example.java"]}`)
	inputs, err := ReadBuildInputs(path)
	if err != nil {
		t.Fatal(err)
	}
	if inputs.Release != 17 ||
		!slices.Equal(inputs.Sources, []string{filepath.Join(root, "Example.java")}) ||
		!slices.Equal(inputs.ClassPath, []string{filepath.Join(root, "lib", "dependency.jar")}) {
		t.Fatalf("unexpected build inputs: %+v", inputs)
	}
}

func TestAnalyzeTypeReferencesAndPackageCycles(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "Alpha.java"),
		"package example.alpha; public interface Alpha { example.beta.Bravo next(); }")
	writeTestFile(t, filepath.Join(root, "Bravo.java"),
		"package example.beta; public interface Bravo { example.alpha.Alpha previous(); }")
	writeTestFile(t, filepath.Join(root, "Independent.java"),
		"package example.other; import example.alpha.Alpha; public interface Independent {}")

	report, err := Analyze(t.Context(), []string{root}, Options{Java: java, Release: release})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Diagnostics) != 0 || len(report.Files) != 3 {
		t.Fatalf("unexpected dependency report: %+v", report)
	}
	want := []PackageIdentity{{Package: "example.alpha"}, {Package: "example.beta"}}
	if len(report.PackageCycles) != 1 || !slices.Equal(report.PackageCycles[0], want) {
		t.Fatalf("cycles = %+v, want [%+v]", report.PackageCycles, want)
	}
	if !slices.ContainsFunc(report.Files[0].TypeReferences, func(reference TypeReference) bool {
		return reference.Package == "example.beta" && reference.BinaryName == "example.beta.Bravo"
	}) {
		t.Fatalf("missing referenced type: %+v", report.Files[0].TypeReferences)
	}
	if slices.ContainsFunc(report.Files[2].TypeReferences, func(reference TypeReference) bool {
		return reference.BinaryName == "example.alpha.Alpha"
	}) {
		t.Fatalf("unused import became a dependency: %+v", report.Files[2].TypeReferences)
	}
}

func TestAnalyzeRetainsGenericBoundsAndTypeUseAnnotations(t *testing.T) {
	java, release := referenceJDK(t)
	path := filepath.Join(t.TempDir(), "Generic.java")
	writeTestFile(t, path, `package example;
import java.lang.annotation.ElementType;
import java.lang.annotation.Target;
import java.util.List;
@Target(ElementType.TYPE_USE)
@interface Mark {}
class Generic<T extends Comparable<T>> {
    List<@Mark ? super T> items;
    <U extends Number> U select(List<? extends U> source) { return source.get(0); }
}
`)
	report, err := Analyze(t.Context(), []string{path}, Options{Java: java, Release: release})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Diagnostics) != 0 || len(report.Files) != 1 {
		t.Fatalf("unexpected Java report: %+v", report)
	}
	declarations := report.Files[0].Declarations
	members := map[string]Occurrence{}
	for _, declaration := range declarations {
		members[declaration.Name] = declaration
	}
	generic := members["Generic"]
	if len(generic.TypeParameters) != 1 || len(generic.TypeParameters[0].Bounds) != 1 {
		t.Fatalf("lost class type parameters: %+v", generic.TypeParameters)
	}
	bound := generic.TypeParameters[0].Bounds[0]
	if bound.Name != "java.lang.Comparable" || len(bound.Arguments) != 1 ||
		bound.Arguments[0].Name != "T" || bound.Arguments[0].Owner != "example.Generic" {
		t.Fatalf("lost recursive bound: %+v", bound)
	}
	field := members["items"].TypeDescription
	if field == nil || field.Name != "java.util.List" || len(field.Arguments) != 1 {
		t.Fatalf("lost generic field signature: %+v", field)
	}
	wildcard := field.Arguments[0]
	if wildcard.Kind != "WILDCARD" || wildcard.LowerBound == nil || wildcard.LowerBound.Name != "T" ||
		len(wildcard.Annotations) != 1 || wildcard.Annotations[0].Type != "example.Mark" {
		t.Fatalf("lost annotated wildcard: %+v", wildcard)
	}
	method := members["select"]
	if method.TypeDescription == nil || len(method.TypeDescription.Parameters) != 1 ||
		method.TypeDescription.Result == nil || method.TypeDescription.Result.Name != "U" ||
		len(method.TypeParameters) != 1 || len(method.TypeParameters[0].Bounds) != 1 ||
		method.TypeParameters[0].Bounds[0].Name != "java.lang.Number" {
		t.Fatalf("lost method generic signature: %+v", method)
	}
}

func TestAnalyzeUsesBuildEncoding(t *testing.T) {
	java, release := referenceJDK(t)
	path := filepath.Join(t.TempDir(), "NonUTF8.java")
	writeTestFile(t, path, "package example; public interface NonUTF8 { char LETTER = '\xE9'; }")
	report, err := Analyze(t.Context(), []string{path}, Options{
		Java: java, Release: release, Encoding: "ISO-8859-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Diagnostics) != 0 || len(report.Files) != 1 || len(report.Files[0].Declarations) != 2 {
		t.Fatalf("unexpected encoding report: %+v", report)
	}
	field := report.Files[0].Declarations[1]
	if field.Constant == nil || *field.Constant != "233" {
		t.Fatalf("expected ISO-8859-1 character value, got %+v", field)
	}
}
