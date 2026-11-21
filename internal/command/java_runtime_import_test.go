package command

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildUsesChangesInExternalJavaRuntime(t *testing.T) {
	t.Setenv("GOWORK", "off")
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOFLAGS", "-buildvcs=false")
	root := t.TempDir()
	provider := filepath.Join(root, "provider")
	output := filepath.Join(root, "generated")
	const providerModule = "example.test/provider"
	const applicationModule = "example.test/application"
	for _, directory := range []string{provider, output} {
		if err := os.Mkdir(directory, 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile := func(path, contents string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(filepath.Join(provider, "go.mod"), "module "+providerModule+"\n\ngo 1.27\n")
	writeFile(filepath.Join(output, "go.mod"), fmt.Sprintf("module %s\n\ngo 1.27\n\nrequire %s v0.0.0\nreplace %s => %q\n",
		applicationModule, providerModule, providerModule, filepath.ToSlash(provider)))
	source := filepath.Join(root, "main.gpp")
	writeFile(source, `package main
import (
    "fmt"
    "gpp/java"
)
func main() { fmt.Println(java.DoubleString(1.0)) }
`)
	binary := filepath.Join(root, "app")
	build := []string{"build", source, "-module", applicationModule, "-output", output, "-o", binary}
	for index, want := range []string{"first", "second"} {
		writeFile(filepath.Join(provider, "runtime.go"), fmt.Sprintf(`package canonical
func Raise(err error) { if err != nil { panic(err) } }
func UnwrapRaised(any) (error, bool) { return nil, false }
func DoubleString(float64) string { return %q }
`, want))
		args := append(append([]string(nil), build...), "-java-runtime-import", providerModule)
		if index == 1 {
			args = append(append([]string(nil), build...), "--java-runtime-import="+providerModule)
		}
		if code := Run(args); code != 0 {
			t.Fatalf("gpp build failed with exit code %d", code)
		}
		command := exec.CommandContext(t.Context(), binary)
		actual, err := command.CombinedOutput()
		if err != nil || strings.TrimSpace(string(actual)) != want {
			t.Fatalf("external runtime output %q, want %q, error %v", actual, want, err)
		}
	}
}
