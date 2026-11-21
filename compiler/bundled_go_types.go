package compiler

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"slices"
	"sync"
)

var bundledGoTypePackages sync.Map

// bundledGoTypeImporter resolves the ordinary Go declarations of virtual GPP
// standard packages before the generated Go module exists. Method bodies and
// GPP-only declarations are outside this type check.
type bundledGoTypeImporter map[string]bool

func (importer bundledGoTypeImporter) Import(importPath string) (*types.Package, error) {
	if !slices.Contains(OfficialStdlibPackages(), importPath) {
		return importNativePackage(importPath)
	}
	if cached, ok := bundledGoTypePackages.Load(importPath); ok {
		return cached.(*types.Package), nil
	}
	if importer[importPath] {
		return nil, fmt.Errorf("import cycle checking bundled Go declarations in %s", importPath)
	}
	importer[importPath] = true
	defer delete(importer, importPath)

	sources, err := loadOfficialPackage(importPath)
	if err != nil {
		return nil, err
	}
	fileSet := token.NewFileSet()
	files := make([]*ast.File, 0, len(sources))
	for _, source := range sources {
		file, err := parseBundledGoDeclarations(source, fileSet)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	configuration := types.Config{Importer: importer, IgnoreFuncBodies: true, DisableUnusedImportCheck: true}
	pkg, err := configuration.Check(importPath, fileSet, files, nil)
	if err != nil {
		return nil, err
	}
	// Concurrent callers must observe the same named Go type identities.
	actual, _ := bundledGoTypePackages.LoadOrStore(importPath, pkg)
	return actual.(*types.Package), nil
}

func parseBundledGoDeclarations(source *File, fileSet *token.FileSet) (*ast.File, error) {
	var code bytes.Buffer
	fmt.Fprintf(&code, "package %s\n", goPackageName(source.Package))
	for _, declaration := range source.Decls {
		var nodes []ast.Decl
		var positions *token.FileSet
		switch value := declaration.(type) {
		case *GoDecl:
			nodes, positions = value.Declarations, value.FileSet
		case *MixedDecl:
			nodes, positions = value.GoASTDecls, value.GoASTFileSet
		case *FunctionDecl:
			if value.GoAST != nil {
				nodes, positions = []ast.Decl{value.GoAST}, value.GoASTFileSet
			}
		}
		for _, node := range nodes {
			if function, ok := node.(*ast.FuncDecl); ok {
				signature := *function
				signature.Body = nil
				node = &signature
			}
			if err := format.Node(&code, positions, node); err != nil {
				return nil, err
			}
			code.WriteByte('\n')
		}
	}
	return parser.ParseFile(fileSet, source.Name, code.Bytes(), parser.SkipObjectResolution)
}
