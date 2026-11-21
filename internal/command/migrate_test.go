package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/telgatech/gpp/internal/javaport"
)

func TestWriteJavaPackagesPreservesExistingOutput(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "translated")
	input := []javaport.EmittedPackage{
		{Name: "example.base", Source: "package example.base\n"},
		{Name: "example.extension", Source: "package example.extension\n"},
	}
	if err := writeJavaPackages(destination, input); err != nil {
		t.Fatal(err)
	}
	if err := writeJavaPackages(destination, []javaport.EmittedPackage{
		{Name: "example.newpackage", Source: "package example.newpackage\n"},
	}); err == nil {
		t.Fatal("expected a nonempty output directory to be preserved")
	}
	for _, pkg := range input {
		path := filepath.Join(destination, filepath.FromSlash(strings.ReplaceAll(pkg.Name, ".", "/")), "translated.gpp")
		if data, err := os.ReadFile(path); err != nil || string(data) != pkg.Source {
			t.Fatalf("unexpected contents at %s: %q, %v", path, data, err)
		}
	}
	if _, err := os.Stat(filepath.Join(destination, "example", "newpackage")); !os.IsNotExist(err) {
		t.Fatalf("failed translation left output in the destination: %v", err)
	}
}
