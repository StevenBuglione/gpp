package command

import (
	"bytes"
	"debug/buildinfo"
	"debug/dwarf"
	"debug/elf"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDebugBuildRetainsGPPSourceAndLocals(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("this test inspects a Linux ELF file")
	}
	t.Setenv("GOOS", runtime.GOOS)
	t.Setenv("GOARCH", runtime.GOARCH)
	t.Setenv("CGO_ENABLED", "0")
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "-trimpath -ldflags=-s -buildvcs=false")

	root := filepath.Join(t.TempDir(), "project with spaces")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "counter.gpp")
	const program = `package main

class Counter {
    func Calculate(seed int) int {
        subtotal := seed + 4
        increment(&subtotal)
        return subtotal * 2
    }
}

func increment(value *int) {
    *value += 1
}

func main() {
    example := Counter()
    println(example.Calculate(17))
}
`
	if err := os.WriteFile(source, []byte(program), 0644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "generated")
	binaryPath := filepath.Join(root, "counter")
	args := []string{"build", source, "-module", "example.com/debug-build", "-no-prelude", "-no-stdlib", "-output", output, "-o", binaryPath}
	build := func(args []string) {
		t.Helper()
		if code := Run(args); code != 0 {
			t.Fatalf("gpp build failed with status %d", code)
		}
	}
	readManifest := func(name string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(output, name))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}

	build(args)
	checkStrippedELF(t, binaryPath)
	releaseArtifact := readManifest(artifactCacheManifestName)
	generatedCache := readManifest(buildCacheManifestName)

	debugArgs := append(append([]string{}, args...), "--debug")
	build(debugArgs)
	checkDebugELF(t, binaryPath, source)
	if !bytes.Equal(readManifest(buildCacheManifestName), generatedCache) {
		t.Fatal("the debug build should reuse the generated Go cache")
	}
	debugArtifact := readManifest(artifactCacheManifestName)
	if bytes.Equal(debugArtifact, releaseArtifact) {
		t.Fatal("debug build reused the release binary")
	}

	build(debugArgs)
	if !bytes.Equal(readManifest(artifactCacheManifestName), debugArtifact) {
		t.Fatal("a repeated debug build should reuse the debug binary")
	}

	build(args)
	checkStrippedELF(t, binaryPath)
}

func checkStrippedELF(t *testing.T, path string) {
	t.Helper()
	file, err := elf.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if file.Section(".debug_info") != nil {
		t.Fatal("ordinary build should honor the inherited stripping flag")
	}
}

func checkDebugELF(t *testing.T, path, source string) {
	t.Helper()
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var compilerFlags string
	for _, setting := range info.Settings {
		if setting.Key == "-gcflags" {
			compilerFlags = setting.Value
		}
	}
	if compilerFlags != "all=-N -l" {
		t.Fatalf("debug build compiler flags = %q", compilerFlags)
	}

	file, err := elf.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	data, err := file.DWARF()
	if err != nil {
		t.Fatalf("read DWARF for debug build: %v", err)
	}
	reader := data.Reader()
	var unit *dwarf.Entry
	for {
		entry, err := reader.Next()
		if err != nil {
			t.Fatal(err)
		}
		if entry == nil {
			t.Fatal("no DWARF function for Counter.Calculate")
		}
		if entry.Tag == dwarf.TagCompileUnit {
			unit = entry
		}
		if entry.Tag != dwarf.TagSubprogram || entry.Val(dwarf.AttrName) != "main.(*Counter).Calculate" {
			continue
		}
		if !dwarfLineAtFunction(t, data, unit, entry, source, 7) {
			t.Fatalf("no address in Counter.Calculate maps to %s:7", source)
		}
		for depth := 1; entry.Children && depth > 0; {
			local, err := reader.Next()
			if err != nil {
				t.Fatal(err)
			}
			if local == nil {
				break
			}
			if local.Tag == dwarf.TagVariable && local.Val(dwarf.AttrName) == "subtotal" && local.Val(dwarf.AttrLocation) != nil {
				return
			}
			switch {
			case local.Tag == 0:
				depth--
			case local.Children:
				depth++
			}
		}
		t.Fatal("Counter.Calculate lacks a debugger location for subtotal")
	}
}

func dwarfLineAtFunction(t *testing.T, data *dwarf.Data, unit, function *dwarf.Entry, source string, line int) bool {
	t.Helper()
	addresses, err := data.Ranges(function)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := data.LineReader(unit)
	if err != nil {
		t.Fatal(err)
	}
	if lines == nil {
		return false
	}
	for {
		var entry dwarf.LineEntry
		if err := lines.Next(&entry); errors.Is(err, io.EOF) {
			return false
		} else if err != nil {
			t.Fatal(err)
		}
		if entry.EndSequence || entry.File == nil || entry.File.Name != source || entry.Line != line {
			continue
		}
		for _, bounds := range addresses {
			if entry.Address >= bounds[0] && entry.Address < bounds[1] {
				return true
			}
		}
	}
}
