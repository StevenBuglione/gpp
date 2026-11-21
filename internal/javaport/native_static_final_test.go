package javaport

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestNativeStaticFinalInitializationMatchesJava(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	reference := filepath.Join(root, "Probe.java")
	writeTestFile(t, reference, `class Probe {
    static String events = "";
    static int record(int value) { events += value; return value; }
    static boolean choose() { return record(2) == 2; }

    static class Holder {
        int Value;
        Holder(int value) { Value = value; }
    }
    static class Base {
        static final Holder Resource = new Holder(record(1));
    }
    static class Leaf extends Base {
        static final int Divisor;
        static { if (choose()) Divisor = 12; else Divisor = 21; }

        static final int Mode;
        static { switch (Divisor) { case 12 -> Mode = record(3); default -> Mode = 8; } }

        static final int Retry;
        static { try { record(4); } finally { Retry = 9; } }

        static final int Port;
        static { for (;;) { Port = record(5); break; } }

        static final int Checked;
        static { if (true) Checked = record(6); }
    }
    public static void main(String[] args) {
        System.out.println(Leaf.class.getSimpleName() + ":" + events);
        Holder item = Leaf.Resource;
        item.Value += 11;
        System.out.println(Leaf.Resource.Value + " " + events);
        System.out.println(Leaf.Divisor + " " + Leaf.Mode + " " + Leaf.Retry + " " + Leaf.Port + " " + Leaf.Checked + " " + events);
    }
}`)
	classes := filepath.Join(root, "classes")
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"),
		"--release", strconv.Itoa(release), "-d", classes, reference)
	want := runObjectTestCommand(t, root, java, "-cp", classes, "Probe")

	base := filepath.Join(root, "base.gpp")
	writeTestFile(t, base, `package reference.final.base

import "strconv"

var Events string

func Record(value int) int {
    Events += strconv.Itoa(value)
    return value
}

class Holder {
    Value int
}

class Base {
    static final Resource *Holder = Holder(Value: Record(1))
}
`)
	leaf := filepath.Join(root, "leaf.gpp")
	writeTestFile(t, leaf, `package reference.final.leaf

import reference.final.base

func Choose() bool { return base.Record(2) == 2 }

class Leaf : base.Base {
    static final Divisor int
    static {
        if Choose() { Divisor = 12 } else { Divisor = 21 }
    }

    static final Mode int
    static {
        switch Divisor {
        case 12: Mode = base.Record(3)
        default: Mode = 8
        }
    }

    static final Retry int
    static {
        try { base.Record(4) } finally { Retry = 9 }
    }

    static final Port int
    static {
        for { Port = base.Record(5); break }
    }

    static final Checked int
    static { if true { Checked = base.Record(6) } }

    // A field named final keeps the existing mutable-field syntax.
    static final int = 77
    static func Legacy() int { return final }
}

func ClassName() string { return Leaf.class.Name }
func Resource() *base.Holder { return Leaf.Resource }
func Adjust() { Leaf.Resource.Value++ }
`)
	output := filepath.Join(root, "output")
	if err := compiler.CompileFilesWithOptions([]string{base, leaf}, output,
		compiler.CompileOptions{ModulePath: "example.com/static-final", IncrementalOutput: true}); err != nil {
		t.Fatal(err)
	}
	check, err := os.ReadFile(filepath.Join(output, "reference", "final", "base", "base.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(check), "GppSetStatic_Base_Resource") || strings.Contains(string(check), "GppAddrStatic_Base_Resource") {
		t.Fatal("static final field was exposed as writable Go storage")
	}
	writeTestFile(t, filepath.Join(output, "main.go"), `package main

import (
    "fmt"
    "example.com/static-final/reference/final/base"
    "example.com/static-final/reference/final/leaf"
)

func main() {
    fmt.Println(leaf.ClassName() + ":" + base.Events)
    leaf.Resource().Value += 10
    leaf.Adjust()
    fmt.Println(base.GppGetStatic_Base_Resource().Value, base.Events)
    fmt.Println(leaf.GppGetStatic_Leaf_Divisor(), leaf.GppGetStatic_Leaf_Mode(),
        leaf.GppGetStatic_Leaf_Retry(), leaf.GppGetStatic_Leaf_Port(),
        leaf.GppGetStatic_Leaf_Checked(), base.Events)
    if leaf.GppStatic_Leaf_Legacy() != 77 { panic("field named final changed") }
}
`)
	got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", ".")
	if got != want {
		t.Fatalf("GPP and Java final initialization diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}

func TestNativeStaticFinalInitializationDiagnosticsMatchJava(t *testing.T) {
	java, release := referenceJDK(t)
	tests := []struct {
		name, java, gpp, diagnostic string
	}{
		{"missing", "static final int V;",
			"static final V int", "might not be initialized"},
		{"missing branch", "static final int V; static { if (choose()) V = 1; }",
			"static final V int; static { if Choose() { V = 1 } }", "might not be initialized"},
		{"read before write", "static final int V; static { int observed = V; V = 1; }",
			"static final V int; static { _ = V; V = 1 }", "might not be initialized"},
		{"second assignment", "static final int V; static { V = 1; V = 2; }",
			"static final V int; static { V = 1; V = 2 }", "might already be assigned"},
		{"loop back edge", "static final int V; static { while (choose()) V = 1; }",
			"static final V int; static { for Choose() { V = 1 } }", "might already be assigned"},
		{"qualified write", "static final int V; static { Probe.V = 1; }",
			"static final V int; static { Probe.V = 1 }", "cannot assign to static final field Probe.V using a qualified name"},
		{"write after initialization", "static final int V = 1; static void change() { V = 3; }",
			"static final V int = 1; static func Change() { V = 3 }", "cannot modify static final field"},
		{"compound write", "static final int V = 1; static { V++; }",
			"static final V int = 1; static { V++ }", "cannot modify static final field"},
		{"closure assignment", "static final int V; static { Runnable set = () -> { V = 1; }; set.run(); }",
			"static final V int; static { Set := func() { V = 1 }; Set() }", "in a function literal"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			reference := filepath.Join(root, "Probe.java")
			writeTestFile(t, reference, "class Probe { static boolean choose() { return true; }\n"+test.java+"\n}")
			javac := exec.CommandContext(t.Context(), filepath.Join(filepath.Dir(java), "javac"),
				"--release", strconv.Itoa(release), "-d", root, reference)
			if output, err := javac.CombinedOutput(); err == nil || !strings.Contains(string(output), "variable V") {
				t.Fatalf("expected javac to reject misuse of V: %v\n%s", err, output)
			}
			source := "package main\nfunc Choose() bool { return true }\nclass Probe {\n" + test.gpp + "\n}"
			file, err := compiler.ParseFile("probe.gpp", source)
			if err != nil {
				t.Fatal(err)
			}
			_, err = compiler.Emit(file)
			if err == nil || !strings.Contains(err.Error(), test.diagnostic) {
				t.Fatalf("expected %q, got %v", test.diagnostic, err)
			}
		})
	}
}

func TestNativeStaticFinalRejectsAddressAndImportedWrite(t *testing.T) {
	for _, test := range []struct{ source, diagnostic string }{
		{"func bad() *int { return &target.Numbers.Value }", "cannot modify static final field Numbers.Value"},
		{"func bad() { target.Numbers.Value = 2 }", "cannot modify static final field Numbers.Value"},
	} {
		root := t.TempDir()
		target := filepath.Join(root, "target.gpp")
		source := filepath.Join(root, "main.gpp")
		writeTestFile(t, target, "package reference.final.target\nclass Numbers { static final Value int = 1 }")
		writeTestFile(t, source, "package main\nimport reference.final.target\n"+test.source)
		err := compiler.CompileFilesWithOptions([]string{source, target}, filepath.Join(root, "output"),
			compiler.CompileOptions{ModulePath: "example.com/static-final"})
		if err == nil || !strings.Contains(err.Error(), test.diagnostic) {
			t.Fatalf("expected %q, got %v", test.diagnostic, err)
		}
	}
}
