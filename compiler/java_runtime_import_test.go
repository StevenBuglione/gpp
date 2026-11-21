package compiler

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJavaRuntimeImportSwitchesIncrementalOutput(t *testing.T) {
	source := filepath.Join(t.TempDir(), "bridge.gpp")
	if err := os.WriteFile(source, []byte(`package example.bridge
import "gpp/java"

func Literal() *java.String { return java.InternStringLiteral("") }
`), 0644); err != nil {
		t.Fatal(err)
	}
	output := t.TempDir()
	generated := filepath.Join(output, "example", "bridge", "bridge.go")
	localRuntime := filepath.Join(output, "gpp", "java", "string.go")
	options := CompileOptions{ModulePath: "example.test/bridge", IncrementalOutput: true}
	for _, provider := range []string{"", "example.test/provider/runtime", "example.test/another/runtime", ""} {
		options.JavaRuntimeImport = provider
		if err := CompileFilesWithOptions([]string{source}, output, options); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(generated)
		if err != nil {
			t.Fatal(err)
		}
		_, runtimeErr := os.Stat(localRuntime)
		if provider == "" {
			if !strings.Contains(string(body), `"example.test/bridge/gpp/java"`) || runtimeErr != nil {
				t.Fatalf("expected bundled runtime, found %v\n%s", runtimeErr, body)
			}
		} else if !strings.Contains(string(body), `java "`+provider+`"`) || !errors.Is(runtimeErr, fs.ErrNotExist) {
			t.Fatalf("expected only the external runtime %s, found %v\n%s", provider, runtimeErr, body)
		}
	}
	options.JavaRuntimeImport = options.ModulePath + "/gpp/java"
	if err := CompileFilesWithOptions([]string{source}, output, options); err == nil ||
		!strings.Contains(err.Error(), "the generated module's own runtime") {
		t.Fatalf("binding to this module's generated runtime should fail, got %v", err)
	}
}

func TestJavaExceptionRuntimeSelectionRefreshesOtherPackages(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "output")
	base := filepath.Join(root, "base.gpp")
	java := filepath.Join(root, "java.gpp")
	for path, source := range map[string]string{
		base: "package base\nfunc Plain() {}\n",
		java: "package example.consumer\nimport \"gpp/java\"\nfunc Empty() *java.String { return java.InternStringLiteral(\"\") }\n",
	} {
		if err := os.WriteFile(path, []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
	}
	options := CompileOptions{ModulePath: "example.test/workspace", IncrementalOutput: true}
	for _, input := range []struct {
		files []string
		want  string
	}{
		{[]string{base}, "type __gppThrownError struct"},
		{[]string{base, java}, `__gppJavaRuntime "example.test/workspace/gpp/java"`},
		{[]string{base}, "type __gppThrownError struct"},
	} {
		if err := CompileFilesWithOptions(input.files, output, options); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(filepath.Join(output, "base", "base.go"))
		if err != nil || !strings.Contains(string(body), input.want) {
			t.Fatalf("generated runtime did not change with the Java input: %v\n%s", err, body)
		}
	}
}
