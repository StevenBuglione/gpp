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
