package lsp

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestQualifiedInheritedStaticMembersOverLSP(t *testing.T) {
	root := t.TempDir()
	const mainSource = `package main

import models "example.com/staticmembers/models"

class Child {
    static const UnqualifiedOnly int = 1

    static func Probe() {
        _ = models.Child.Count
        _ = models.Child.Limit
        _ = models.Child.Inspect()
        _ = models.Child.hidden
    }
}
`
	const childSource = `package family

import origin "example.com/staticmembers/source"

class Child : origin.Parent {
    static const Count int = 10
    static hidden int = 2
    Payload int

    static func localProbe() int { return Child._Threshold }
}
`
	const parentSource = `package shared

class Parent {
    static Count int = 1
    static const Limit int = 6
    static const _Threshold int = 2
    static func Inspect() int { return 9 }
}
`
	files := map[string]string{
		"go.mod":            "module example.com/staticmembers\n\ngo 1.27\n",
		"main.gpp":          mainSource,
		"models/child.gpp":  childSource,
		"source/parent.gpp": parentSource,
		"other/parent.gpp":  "package shared\nclass Parent { static const WrongPackage int = 12 }\n",
	}
	for name, source := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
	}
	uri := uriForPath(filepath.Join(root, "main.gpp"))
	var input, output bytes.Buffer
	writeTestMessage(&input, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"rootUri": uriForPath(root)}})
	writeTestMessage(&input, map[string]any{"jsonrpc": "2.0", "method": "initialized"})
	const receiver = "models.Child."
	requestAt := func(id int, method, member string) {
		writeTestMessage(&input, map[string]any{
			"jsonrpc": "2.0", "id": id, "method": "textDocument/" + method,
			"params": textDocumentPositionParams{
				TextDocument: textDocumentIdentifier{URI: uri},
				Position:     positionAtOffset(mainSource, strings.Index(mainSource, receiver+member)+len(receiver)),
			},
		})
	}
	requestAt(2, "completion", "Count")
	requestAt(3, "definition", "Count")
	requestAt(4, "definition", "Limit")
	requestAt(5, "definition", "Inspect")
	requestAt(6, "hover", "Limit")
	requestAt(7, "definition", "hidden")
	writeTestMessage(&input, map[string]any{
		"jsonrpc": "2.0", "id": 8, "method": "textDocument/definition",
		"params": textDocumentPositionParams{
			TextDocument: textDocumentIdentifier{URI: uriForPath(filepath.Join(root, "models/child.gpp"))},
			Position:     positionAtOffset(childSource, strings.Index(childSource, "_Threshold")),
		},
	})
	writeTestMessage(&input, map[string]any{"jsonrpc": "2.0", "id": 9, "method": "shutdown"})
	writeTestMessage(&input, map[string]any{"jsonrpc": "2.0", "method": "exit"})
	if err := Run(&input, &output, io.Discard); err != nil {
		t.Fatal(err)
	}
	results := map[int]any{}
	for _, message := range readTestMessages(t, output.Bytes()) {
		if id, ok := message["id"].(float64); ok {
			if message["error"] != nil {
				t.Fatalf("request %g returned an error: %v", id, message["error"])
			}
			results[int(id)] = message["result"]
		}
	}
	completions, _ := results[2].([]any)
	items := map[string]any{}
	for _, completion := range completions {
		item := completion.(map[string]any)
		items[item["label"].(string)] = item["detail"]
	}
	if len(items) != 3 || items["Count"] != "static const Count int" || items["Limit"] != "static const Limit int" || items["Inspect"] != "static func Child.Inspect() int" {
		t.Errorf("completion should show the qualified class and its visible inherited members: %v", items)
	}
	for _, definition := range []struct {
		id       int
		file     string
		position Position
	}{
		{3, "models/child.gpp", positionAtOffset(childSource, strings.Index(childSource, "Count int"))},
		{4, "source/parent.gpp", positionAtOffset(parentSource, strings.Index(parentSource, "Limit int"))},
		{5, "source/parent.gpp", positionAtOffset(parentSource, strings.Index(parentSource, "Inspect()"))},
		{8, "source/parent.gpp", positionAtOffset(parentSource, strings.Index(parentSource, "_Threshold int"))},
	} {
		locations, _ := results[definition.id].([]any)
		if len(locations) != 1 {
			t.Errorf("definition request %d should have one source location: %v", definition.id, results[definition.id])
			continue
		}
		location := locations[0].(map[string]any)
		start := location["range"].(map[string]any)["start"].(map[string]any)
		if location["uri"] != uriForPath(filepath.Join(root, definition.file)) || start["line"] != float64(definition.position.Line) || start["character"] != float64(definition.position.Character) {
			t.Errorf("definition request %d should resolve to %s at %v, got %v", definition.id, definition.file, definition.position, location)
		}
	}
	if hover, ok := results[6].(map[string]any); !ok || !strings.Contains(hover["contents"].(map[string]any)["value"].(string), "static const Limit int") {
		t.Errorf("hover should show the inherited constant signature: %v", results[6])
	}
	if locations, _ := results[7].([]any); len(locations) > 0 {
		t.Errorf("a local class with the same name cannot access a private member: %v", locations)
	}
}

func TestClassConstantCompletionDefinitionAndHighlighting(t *testing.T) {
	source := `package main

class Base {
    static const Limit int = 6
    static Count int = Limit
    Payload int
}

class Child : Base {}

func probe() {
    value := Child()
    _ = Child.Limit
    _ = value.Payload
}
`
	file, err := compiler.ParseFile("main.gpp", source)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "main.gpp")
	file.SourcePath = path
	state := &fileState{Path: path, URI: uriForPath(path), Source: source, File: file}
	workspace := &workspaceState{files: map[string]*fileState{path: state}}
	workspace.docIndex = compiler.BuildDocIndex([]*compiler.File{file})
	workspace.symbols = workspace.collectSymbols()
	server := &server{workspace: workspace}

	selector := strings.Index(source, "Child.Limit")
	items := server.contextualCompletions(state, selector+len("Child."))
	labels := completionLabels(items)
	if !labels["Limit"] || !labels["Count"] || labels["Payload"] {
		t.Fatalf("expected class static members in completion, got %v", labels)
	}
	for _, item := range items {
		if item.Label == "Limit" && (item.Kind != 21 || item.Detail != "static const Limit int") {
			t.Fatalf("expected a typed constant completion, got %#v", item)
		}
	}
	receiver := strings.Index(source, "value.Payload") + len("value.")
	instance := completionLabels(server.contextualCompletions(state, receiver))
	if instance["Limit"] || instance["Count"] || !instance["Payload"] {
		t.Fatalf("expected instance members in completion, got %v", instance)
	}

	use := selector + len("Child.")
	symbols := server.resolveSymbolsAt(state.URI, positionAtOffset(source, use), "Limit")
	if len(symbols) != 1 || symbols[0].Symbol.Parent != "Base" || !symbols[0].Symbol.Constant {
		t.Fatalf("expected the inherited constant declaration, got %#v", symbols)
	}
	declaration := strings.Index(source, "Limit int")
	span, found := astSymbolNameSpanInDecl(file, file.Decls[0], symbols[0].Symbol)
	if !found || span.Start != declaration {
		t.Fatalf("expected the constant definition at %d, got %+v", declaration, span)
	}

	tokens := map[int]semanticToken{}
	classifyGPPTokens(file, tokens)
	want := modifierDeclaration | modifierStatic | modifierReadonly
	if classification := tokens[declaration]; classification.tokenType != semanticProperty || classification.modifiers != want {
		t.Fatalf("expected a static readonly property, got %#v", classification)
	}
}
