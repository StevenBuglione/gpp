package javaport

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

//go:embed JavaSourceAnalysis.java
var analyzerSource []byte

type Options struct {
	Java       string
	Release    int
	ClassPath  string
	ModulePath string
	Preview    bool
}

type BuildInputs struct {
	SchemaVersion int      `json:"schemaVersion"`
	Release       int      `json:"release"`
	Preview       bool     `json:"preview"`
	ClassPath     []string `json:"classPath"`
	ModulePath    []string `json:"modulePath"`
	Sources       []string `json:"sources"`
}

func ReadBuildInputs(path string) (BuildInputs, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return BuildInputs{}, err
	}
	var inputs BuildInputs
	if err := json.Unmarshal(data, &inputs); err != nil {
		return BuildInputs{}, fmt.Errorf("read Java build inputs %s: %w", path, err)
	}
	if inputs.SchemaVersion != 1 {
		return BuildInputs{}, fmt.Errorf("unsupported Java build input schema %d", inputs.SchemaVersion)
	}
	for _, paths := range [][]string{inputs.Sources, inputs.ClassPath, inputs.ModulePath} {
		for index, entry := range paths {
			if entry == "" || strings.ContainsRune(entry, '\x00') {
				return BuildInputs{}, fmt.Errorf("invalid path in Java build inputs %s", path)
			}
			if !filepath.IsAbs(entry) {
				paths[index] = filepath.Join(filepath.Dir(path), entry)
			}
		}
	}
	return inputs, nil
}

type Report struct {
	SchemaVersion int          `json:"schemaVersion"`
	Release       int          `json:"release"`
	Preview       bool         `json:"preview"`
	Files         []SourceFile `json:"files"`
	Diagnostics   []Diagnostic `json:"diagnostics"`
}

type SourceFile struct {
	Path         string       `json:"path"`
	Package      string       `json:"package"`
	Declarations []Occurrence `json:"declarations"`
	Calls        []Occurrence `json:"calls"`
}

type Occurrence struct {
	Kind            string      `json:"kind"`
	Symbol          string      `json:"symbol"`
	Type            string      `json:"type"`
	Owner           string      `json:"owner,omitempty"`
	Name            string      `json:"name,omitempty"`
	Modifiers       []string    `json:"modifiers,omitempty"`
	Parameters      []Parameter `json:"parameters,omitempty"`
	Result          string      `json:"result,omitempty"`
	Thrown          []string    `json:"thrown,omitempty"`
	Supertypes      []string    `json:"supertypes,omitempty"`
	HasTypeParams   bool        `json:"hasTypeParams,omitempty"`
	Constant        *string     `json:"constant,omitempty"`
	AnnotationCount int         `json:"annotationCount,omitempty"`
	// Start and End are Java compiler offsets in UTF-16 code units.
	Start  int64 `json:"start"`
	End    int64 `json:"end"`
	Line   int64 `json:"line"`
	Column int64 `json:"column"`
}

type Parameter struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type Diagnostic struct {
	Kind    string `json:"kind"`
	Code    string `json:"code"`
	Path    string `json:"path"`
	Line    int64  `json:"line"`
	Column  int64  `json:"column"`
	Message string `json:"message"`
}

func Analyze(ctx context.Context, inputs []string, options Options) (Report, error) {
	sources, err := javaSources(inputs)
	if err != nil {
		return Report{}, err
	}
	if options.Release < 7 {
		return Report{}, fmt.Errorf("unsupported Java source release %d", options.Release)
	}
	if options.Java == "" {
		options.Java = "java"
	}
	launcher, err := exec.LookPath(options.Java)
	if err != nil {
		return Report{}, fmt.Errorf("find Java launcher (a JDK 17 or newer is required): %w", err)
	}
	scratch, err := os.MkdirTemp("", "gpp-java-analysis-")
	if err != nil {
		return Report{}, err
	}
	defer os.RemoveAll(scratch)

	analyzer := filepath.Join(scratch, "JavaSourceAnalysis.java")
	if err := os.WriteFile(analyzer, analyzerSource, 0600); err != nil {
		return Report{}, err
	}

	// Framing with NUL keeps file paths with whitespace intact and permits
	// projects whose source paths exceed the OS command-line limit.
	arguments := append([]string{
		strconv.Itoa(options.Release), strconv.FormatBool(options.Preview), options.ClassPath, options.ModulePath,
	}, sources...)
	var stderr bytes.Buffer
	command := exec.CommandContext(ctx, launcher, analyzer)
	command.Stdin = strings.NewReader(strings.Join(arguments, "\x00"))
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		if message := strings.TrimSpace(stderr.String()); message != "" {
			return Report{}, fmt.Errorf("Java source analysis: %w\n%s", err, message)
		}
		return Report{}, fmt.Errorf("Java source analysis: %w", err)
	}
	var report Report
	if err := json.Unmarshal(output, &report); err != nil {
		return Report{}, fmt.Errorf("read Java analysis report: %w", err)
	}
	if report.SchemaVersion != 1 {
		return Report{}, fmt.Errorf("unsupported Java analysis schema %d", report.SchemaVersion)
	}
	return report, nil
}

func javaSources(inputs []string) ([]string, error) {
	var sources []string
	for _, input := range inputs {
		path, err := filepath.Abs(input)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			if !info.Mode().IsRegular() || filepath.Ext(path) != ".java" {
				return nil, fmt.Errorf("expected a Java source file or directory: %s", input)
			}
			sources = append(sources, path)
			continue
		}
		err = filepath.WalkDir(path, func(entryPath string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() && entryPath != path && entry.Name() == ".git" {
				return filepath.SkipDir
			}
			if entry.Type().IsRegular() && filepath.Ext(entryPath) == ".java" {
				sources = append(sources, entryPath)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("no Java source files found")
	}
	slices.Sort(sources)
	return slices.Compact(sources), nil
}
