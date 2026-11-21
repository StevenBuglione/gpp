package lsp

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

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
