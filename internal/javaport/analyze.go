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
	Java         string
	Release      int
	Encoding     string
	ClassPath    string
	ModulePath   string
	Preview      bool
	MethodBodies bool
}

type BuildInputs struct {
	SchemaVersion int      `json:"schemaVersion"`
	Release       int      `json:"release"`
	Preview       bool     `json:"preview"`
	Encoding      string   `json:"encoding,omitempty"`
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
	SchemaVersion int                 `json:"schemaVersion"`
	Release       int                 `json:"release"`
	Preview       bool                `json:"preview"`
	Files         []SourceFile        `json:"files"`
	PackageCycles [][]PackageIdentity `json:"packageCycles,omitempty"`
	Diagnostics   []Diagnostic        `json:"diagnostics"`
}

type SourceFile struct {
	Path           string            `json:"path"`
	Package        string            `json:"package"`
	ModuleName     string            `json:"moduleName,omitempty"`
	TypeReferences []TypeReference   `json:"typeReferences,omitempty"`
	Declarations   []Occurrence      `json:"declarations"`
	Calls          []Occurrence      `json:"calls"`
	Module         *ModuleDescriptor `json:"module,omitempty"`
}

type PackageIdentity struct {
	ModuleName string `json:"moduleName,omitempty"`
	Package    string `json:"package"`
}

type TypeReference struct {
	PackageIdentity
	BinaryName string `json:"binaryName"`
}

// ModuleDescriptor preserves explicit module-info.java declarations.
type ModuleDescriptor struct {
	Name       string            `json:"name"`
	Open       bool              `json:"open"`
	Directives []ModuleDirective `json:"directives"`
}

type ModuleDirective struct {
	Kind       string   `json:"kind"`
	Name       string   `json:"name"`
	Targets    []string `json:"targets,omitempty"`
	Static     bool     `json:"static,omitempty"`
	Transitive bool     `json:"transitive,omitempty"`
}

type Occurrence struct {
	Kind                     string           `json:"kind"`
	Symbol                   string           `json:"symbol"`
	Type                     string           `json:"type"`
	Owner                    string           `json:"owner,omitempty"`
	Name                     string           `json:"name,omitempty"`
	Modifiers                []string         `json:"modifiers,omitempty"`
	Parameters               []Parameter      `json:"parameters,omitempty"`
	Result                   string           `json:"result,omitempty"`
	Thrown                   []string         `json:"thrown,omitempty"`
	Supertypes               []string         `json:"supertypes,omitempty"`
	HasTypeParams            bool             `json:"hasTypeParams,omitempty"`
	Constant                 *string          `json:"constant,omitempty"`
	ConstantUTF16LE          *string          `json:"constantUtf16LE,omitempty"`
	AnnotationCount          int              `json:"annotationCount,omitempty"`
	Annotations              []Annotation     `json:"annotations,omitempty"`
	AnnotationDefault        *AnnotationValue `json:"annotationDefault,omitempty"`
	ModuleName               string           `json:"moduleName,omitempty"`
	TypeDescription          *TypeDescription `json:"typeDescription,omitempty"`
	TypeParameters           []TypeParameter  `json:"typeParameters,omitempty"`
	Body                     *MethodBody      `json:"body,omitempty"`
	Initializer              *JavaExpression  `json:"initializer,omitempty"`
	InitializerOwner         string           `json:"initializerOwner,omitempty"`
	StaticInitializer        bool             `json:"staticInitializer,omitempty"`
	ConstructorCall          *JavaExpression  `json:"constructorCall,omitempty"`
	RecordComponents         []Parameter      `json:"recordComponents,omitempty"`
	CompactRecordConstructor bool             `json:"compactRecordConstructor,omitempty"`
	VariableArity            bool             `json:"variableArity,omitempty"`
	Synthetic                bool             `json:"synthetic,omitempty"`
	// Start and End are Java compiler offsets in UTF-16 code units.
	Start  int64 `json:"start"`
	End    int64 `json:"end"`
	Line   int64 `json:"line"`
	Column int64 `json:"column"`
}

type MethodBody struct {
	Code []JavaStatement `json:"code"`
}

type JavaStatement struct {
	Kind       string           `json:"kind"`
	Expression *JavaExpression  `json:"expression,omitempty"`
	Detail     *JavaExpression  `json:"detail,omitempty"`
	Body       *MethodBody      `json:"body,omitempty"`
	Catches    []JavaCatch      `json:"catches,omitempty"`
	Finally    *MethodBody      `json:"finally,omitempty"`
	Resources  []JavaVariable   `json:"resources,omitempty"`
	Variable   *JavaVariable    `json:"variable,omitempty"`
	Then       *JavaStatement   `json:"then,omitempty"`
	Else       *JavaStatement   `json:"else,omitempty"`
	Nested     *JavaStatement   `json:"nested,omitempty"`
	LoopInit   *MethodBody      `json:"loopInit,omitempty"`
	LoopUpdate *MethodBody      `json:"loopUpdate,omitempty"`
	Label      string           `json:"label,omitempty"`
	Cases      []JavaSwitchCase `json:"cases,omitempty"`
	Line       int64            `json:"line"`
	Column     int64            `json:"column"`
}

type JavaVariable struct {
	Name        string          `json:"name,omitempty"`
	Type        string          `json:"type"`
	Initializer *JavaExpression `json:"initializer,omitempty"`
}

type JavaCatch struct {
	Name  string      `json:"name"`
	Types []string    `json:"types"`
	Body  *MethodBody `json:"body"`
}

type JavaSwitchCase struct {
	Labels  []*JavaExpression `json:"labels"`
	Default bool              `json:"default"`
	Pattern bool              `json:"pattern"`
	Group   *MethodBody       `json:"group,omitempty"`
	Value   *JavaExpression   `json:"value,omitempty"`
	Rule    *JavaStatement    `json:"rule,omitempty"`
	Line    int64             `json:"line"`
	Column  int64             `json:"column"`
}

type JavaExpression struct {
	Kind      string            `json:"kind"`
	Type      string            `json:"type"`
	Name      string            `json:"name,omitempty"`
	BindingID int64             `json:"bindingId,omitempty"`
	Operand   *JavaExpression   `json:"operand,omitempty"`
	Target    *JavaExpression   `json:"target,omitempty"`
	Left      *JavaExpression   `json:"left,omitempty"`
	Right     *JavaExpression   `json:"right,omitempty"`
	Condition *JavaExpression   `json:"condition,omitempty"`
	WhenTrue  *JavaExpression   `json:"whenTrue,omitempty"`
	WhenFalse *JavaExpression   `json:"whenFalse,omitempty"`
	Literal   string            `json:"literal,omitempty"`
	UTF16LE   *string           `json:"utf16LE,omitempty"`
	Symbol    string            `json:"symbol,omitempty"`
	Receiver  string            `json:"receiver,omitempty"`
	Static    bool              `json:"static,omitempty"`
	Arguments []*JavaExpression `json:"arguments,omitempty"`
	Cases     []JavaSwitchCase  `json:"cases,omitempty"`
	Line      int64             `json:"line"`
	Column    int64             `json:"column"`
}

// TypeDescription preserves the generic structure that a JVM descriptor erases.
type TypeDescription struct {
	Kind        string            `json:"kind"`
	Name        string            `json:"name,omitempty"`
	ModuleName  string            `json:"moduleName,omitempty"`
	Owner       string            `json:"owner,omitempty"`
	Arguments   []TypeDescription `json:"arguments,omitempty"`
	Annotations []Annotation      `json:"annotations,omitempty"`
	Enclosing   *TypeDescription  `json:"enclosing,omitempty"`
	Component   *TypeDescription  `json:"component,omitempty"`
	UpperBound  *TypeDescription  `json:"upperBound,omitempty"`
	LowerBound  *TypeDescription  `json:"lowerBound,omitempty"`
	Bounds      []TypeDescription `json:"bounds,omitempty"`
	Parameters  []TypeDescription `json:"parameters,omitempty"`
	Result      *TypeDescription  `json:"result,omitempty"`
	Thrown      []TypeDescription `json:"thrown,omitempty"`
}

type TypeParameter struct {
	Name   string            `json:"name"`
	Bounds []TypeDescription `json:"bounds"`
}

type Parameter struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type Annotation struct {
	Type      string           `json:"type"`
	Retention string           `json:"retention"`
	Members   []AnnotationPair `json:"members"`
}

type AnnotationPair struct {
	Name     string          `json:"name"`
	Explicit bool            `json:"explicit"`
	Value    AnnotationValue `json:"value"`
}

type AnnotationValue struct {
	Kind       string            `json:"kind"`
	Text       string            `json:"text,omitempty"`
	Annotation *Annotation       `json:"annotation,omitempty"`
	Elements   []AnnotationValue `json:"elements,omitempty"`
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
		strconv.Itoa(options.Release), strconv.FormatBool(options.Preview), options.ClassPath, options.ModulePath, options.Encoding, strconv.FormatBool(options.MethodBodies),
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
	report.PackageCycles = packageCycles(report.Files)
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
