package compiler

import (
	"slices"
	"strings"
	"testing"
)

func TestNativeSealedClassDeclarations(t *testing.T) {
	input := `package reference.sealing

// Ordinary Go can still use these names.
const final = 1
var sealed = final + 1

// Root documents the closed hierarchy.
sealed class Root permits Fixed, Open {}
final class Fixed:Root {}
non-sealed class Open : Root {}
class Extra : Open {}
sealed class Implicit {}
final class Inferred : Implicit {}
`
	formatted, err := FormatSourceFile("root.gpp", []byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if string(formatted) != FormatSource(string(formatted)) {
		t.Fatal("sealed class formatting is not idempotent")
	}
	program := parseProgram(t, string(formatted))
	model, err := ResolveProgram(program)
	if err != nil {
		t.Fatal(err)
	}
	classes := model.Packages["reference.sealing"].Classes
	root := classes["Root"]
	if root.InheritanceModifier != "sealed" || !slices.Equal(root.PermittedNames(), []string{"Fixed", "Open"}) {
		t.Fatalf("sealed metadata missing: %+v", root)
	}
	if got := classSource(root); got != "sealed class Root permits Fixed, Open" {
		t.Fatalf("documentation omits the declaration: %q", got)
	}
	if root.Doc != "Root documents the closed hierarchy." || classes["Open"].InheritanceModifier != "non-sealed" {
		t.Fatal("comment or non-sealed modifier was lost")
	}
}

func TestNativeSealedClassErrors(t *testing.T) {
	tests := []struct {
		name    string
		sources []string
		want    string
	}{
		{"unauthorized", []string{"sealed class Root permits Fixed {}\nfinal class Fixed : Root {}\nfinal class Stranger : Root {}\n"}, "Stranger is not permitted"},
		{"unmodified", []string{"sealed class Root permits Fixed {}\nclass Fixed : Root {}\n"}, "must be final, sealed, or non-sealed"},
		{"alone", []string{"non-sealed class Open {}\n"}, "requires a direct sealed parent"},
		{"inherit final", []string{"final class Fixed {}\nclass Child : Fixed {}\n"}, "cannot extend final class Fixed"},
		{"indirect", []string{"sealed class Root permits Open, Child {}\nnon-sealed class Open : Root {}\nclass Child : Open {}\n"}, "Child is not a direct subclass"},
		{"missing", []string{"sealed class Root permits Absent {}\n"}, "unknown permitted subclass Absent"},
		{"duplicate", []string{"sealed class Root permits Fixed, Fixed {}\nfinal class Fixed : Root {}\n"}, "duplicate permitted subclass Fixed"},
		{"inference requires same file", []string{
			"sealed class Root {}\nfinal class SameFile : Root {}\n",
			"final class OtherFile : Root {}\n",
		}, "OtherFile is not permitted"},
		{"empty inference", []string{"sealed class Root {}\n"}, "must permit at least one direct subclass"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var sources []string
			for _, source := range test.sources {
				sources = append(sources, "package p\n"+source)
			}
			if _, err := ResolveProgram(parseProgram(t, sources...)); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ResolveProgram error %v, want %q", err, test.want)
			}
		})
	}
	if _, err := ParseFile("root.gpp", "package p\nfinal class Root permits Other {}\n"); err == nil || !strings.Contains(err.Error(), "only a sealed class can declare permits") {
		t.Fatalf("ParseFile accepted permits on a final class: %v", err)
	}
}
