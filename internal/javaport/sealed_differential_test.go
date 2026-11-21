package javaport

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestJavaAOTSealedTypesMatchOpenJDK(t *testing.T) {
	java, release := referenceJDK(t)
	if release < 21 {
		t.Skip("requires stable record and switch patterns")
	}
	root := t.TempDir()
	var inputs []string
	for name, source := range map[string]string{
		"Base": `public sealed abstract class Base permits Fixed, OpenBase {
    protected Base() {}
    public abstract int evaluate();
    public int times() { return evaluate() * 2; }
}`,
		"Fixed": `public final class Fixed extends Base {
    public int evaluate() { return 4; }
}`,
		"OpenBase": `public non-sealed class OpenBase extends Base {
    public int evaluate() { return 6; }
}`,
		"Extra": `public final class Extra extends OpenBase {
    public int evaluate() { return 8; }
}`,
		"Closed": `public sealed interface Closed permits Direct, Extension {
    int code();
}`,
		"Direct":    `public record Direct(int code) implements Closed {}`,
		"Extension": `public non-sealed interface Extension extends Closed {}`,
		"Indirect": `public final class Indirect implements Extension {
    public int code() { return 11; }
}`,
		"Inferred": `public sealed interface Inferred { int code(); }
final class Implicit implements Inferred { public int code() { return 17; } }`,
		"Structural": `public interface Structural { int code(); }`,
		"Calls": `public class Calls {
    public Inferred inferred() { return new Implicit(); }
    public int code(Closed value) { return value.code(); }
    public int times(Base value) { return value.times(); }
    public boolean isClosed(Object value) { return value instanceof Closed; }
    public int split(Closed value) {
        return switch (value) {
            case Direct(int code) -> code + 100;
            case Extension extension -> extension.code() + 200;
        };
    }
}`,
	} {
		file := filepath.Join(root, name+".java")
		writeTestFile(t, file, "package reference.sealing;\n"+source)
		inputs = append(inputs, file)
	}

	probe := filepath.Join(root, "Probe.java")
	writeTestFile(t, probe, `import java.util.Arrays;
import java.util.stream.Collectors;
import reference.sealing.*;
class Probe {
    static String permits(Class<?> owner) {
        return Arrays.stream(owner.getPermittedSubclasses()).map(Class::getName)
            .collect(Collectors.joining(","));
    }
    public static void main(String[] args) {
        Calls calls = new Calls();
        System.out.println(calls.times(new Fixed()) + " " + calls.times(new OpenBase()) +
            " " + calls.times(new Extra()));
        System.out.println(calls.code(new Direct(7)) + " " + calls.code(new Indirect()) +
            " " + calls.inferred().code());
        System.out.println(calls.split(new Direct(7)) + " " + calls.split(new Indirect()));
        System.out.println(calls.isClosed(new Direct(7)) + " " +
            calls.isClosed(new Structural() { public int code() { return 25; } }));
        System.out.println(permits(Base.class));
        System.out.println(permits(Closed.class));
        System.out.println(permits(Inferred.class));
    }
}`)
	classes := filepath.Join(root, "classes")
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"),
		append([]string{"--release", strconv.Itoa(release), "-sourcepath", classes, "-d", classes, probe}, inputs...)...)
	want := runObjectTestCommand(t, root, java, "-cp", classes, "Probe")

	report, err := Analyze(t.Context(), inputs, Options{Java: java, Release: release, MethodBodies: true})
	if err != nil {
		t.Fatal(err)
	}
	units, err := EmitGPPPackages(report)
	if err != nil {
		t.Fatal(err)
	}
	var sources []string
	for _, unit := range units {
		file := filepath.Join(root, unit.Name+".gpp")
		writeTestFile(t, file, unit.Source)
		sources = append(sources, file)
	}
	generated := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions(sources, generated, compiler.CompileOptions{
		ModulePath: "example.com/java-sealing",
	}); err != nil {
		t.Fatal(err)
	}

	caller := filepath.Join(root, "caller")
	if err := os.Mkdir(caller, 0755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(caller, "go.mod"), `module example.com/sealing-consumer

go 1.27

require example.com/java-sealing v0.0.0

replace example.com/java-sealing => ../generated
`)
	writeTestFile(t, filepath.Join(caller, "main.go"), `package main

import (
    "fmt"
    "strings"
    "example.com/java-sealing/reference/sealing"
)

type structural struct{}
func (structural) Code() int32 { return 25 }

var _ sealing.Structural = structural{}
var _ sealing.Closed = (*sealing.Direct)(nil)
var _ sealing.Extension = (*sealing.Indirect)(nil)

func main() {
    calls := sealing.NewCalls()
    fmt.Println(calls.Times(sealing.NewFixed().GppAsBase()), calls.Times(sealing.NewOpenBase().GppAsBase()),
        calls.Times(sealing.NewExtra().GppAsBase()))
    fmt.Println(calls.Code(sealing.NewDirectWithInt(7)), calls.Code(sealing.NewIndirect()), calls.Inferred().Code())
    fmt.Println(calls.Split(sealing.NewDirectWithInt(7)), calls.Split(sealing.NewIndirect()))
    direct := sealing.NewDirectWithInt(7)
    _, structuralClosed := any(structural{}).(sealing.Closed)
    if structuralClosed { panic("structural Go value satisfied sealed interface") }
    fmt.Println(calls.IsClosed(direct), calls.IsClosed(structural{}))
    first := sealing.GppJavaPermittedSubclasses_Base()
    first[0] = "invalid"
    fmt.Println(strings.Join(sealing.GppJavaPermittedSubclasses_Base(), ","))
    fmt.Println(strings.Join(sealing.GppJavaPermittedSubclasses_Closed(), ","))
    fmt.Println(strings.Join(sealing.GppJavaPermittedSubclasses_Inferred(), ","))
}
`)
	if got := runObjectTestCommand(t, caller, "go", "run", "-buildvcs=false", "-mod=mod", "."); got != want {
		t.Fatalf("GPP and Java sealed types diverged\nGo:\n%s\nJava:\n%s", got, want)
	}

	for index := range report.Files {
		for declaration := range report.Files[index].Declarations {
			owner := &report.Files[index].Declarations[declaration]
			if owner.Symbol == "reference.sealing.Base" && owner.Kind == "CLASS" {
				owner.PermittedSubclasses = owner.PermittedSubclasses[:1]
			}
		}
	}
	if _, err := EmitGPPPackages(report); err == nil || !strings.Contains(err.Error(), "subtype is not permitted") {
		t.Fatalf("emission accepted a contradictory attributed permits list: %v", err)
	}
}

func TestNativeGPPSealedClassesMatchJava(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	probe := filepath.Join(root, "Probe.java")
	writeTestFile(t, probe, `class Probe {
    static sealed class Root permits Fixed, Open {
        int read() { return code(); }
        int code() { return 1; }
    }
    static final class Fixed extends Root { int code() { return 2; } }
    static non-sealed class Open extends Root { int code() { return 3; } }
    static class Extra extends Open { int code() { return 4; } }
    static sealed class Inferred {}
    static final class Implicit extends Inferred {}
    public static void main(String[] args) {
        for (Root value : new Root[]{new Root(), new Fixed(), new Open(), new Extra()}) {
            System.out.println(value.read());
        }
        System.out.println(new Implicit() instanceof Inferred);
    }
}`)
	classes := filepath.Join(root, "classes")
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"),
		"--release", strconv.Itoa(release), "-d", classes, probe)
	want := runObjectTestCommand(t, root, java, "-cp", classes, "Probe")

	base := filepath.Join(root, "base.gpp")
	writeTestFile(t, base, `package reference.sealed.base

sealed class Root permits reference.sealed.child.Fixed, reference.sealed.child.Open {
    func Read() int32 { return this.Code() }
    func Code() int32 { return 1 }
}
func BuildRoot() *Root { return Root() }

sealed class Inferred {}
final class Implicit : Inferred {}
func BuildImplicit() *Implicit { return Implicit() }
`)
	children := filepath.Join(root, "children.gpp")
	writeTestFile(t, children, `package reference.sealed.child

import ancestor "example.com/native-sealing/reference/sealed/base"

final class Fixed : ancestor.Root {
    func Code() int32 { return 2 }
}
non-sealed class Open : ancestor.Root {
    func Code() int32 { return 3 }
}
class Extra : Open {
    func Code() int32 { return 4 }
}
func BuildFixed() *Fixed { return Fixed() }
func BuildOpen() *Open { return Open() }
func BuildExtra() *Extra { return Extra() }
`)
	output := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions([]string{base, children}, output, compiler.CompileOptions{
		ModulePath: "example.com/native-sealing",
	}); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(output, "main.go"), `package main

import (
    "fmt"
    "example.com/native-sealing/reference/sealed/base"
    "example.com/native-sealing/reference/sealed/child"
)

type GoWrapper struct{ child.Fixed }
var _ interface{ Read() int32 } = (*GoWrapper)(nil)

func main() {
    fixed, open, extra := child.BuildFixed(), child.BuildOpen(), child.BuildExtra()
    for _, value := range []*base.Root{base.BuildRoot(), &fixed.Root, &open.Root, &extra.Open.Root} {
        fmt.Println(value.Read())
    }
    var inferred *base.Inferred = &base.BuildImplicit().Inferred
    fmt.Println(inferred != nil)
}
`)
	if got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", "."); got != want {
		t.Fatalf("native GPP and Java sealed classes diverged\nGo:\n%s\nJava:\n%s", got, want)
	}

	unauthorized := filepath.Join(root, "unauthorized.gpp")
	writeTestFile(t, unauthorized, `package reference.sealed.child

import ancestor "example.com/native-sealing/reference/sealed/base"

final class Stranger : ancestor.Root {}
`)
	if err := compiler.CompileFilesWithOptions([]string{base, children, unauthorized}, output, compiler.CompileOptions{
		ModulePath: "example.com/native-sealing",
	}); err == nil || !strings.Contains(err.Error(), "Stranger is not permitted") {
		t.Fatalf("included an unauthorized subclass from an imported GPP package: %v", err)
	}
}

func TestJavaAOTSealedTypesRejectInvalidHierarchy(t *testing.T) {
	java, release := referenceJDK(t)
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "unpermitted class",
			source: "public sealed class Root permits Allowed {} final class Allowed extends Root {} final class Other extends Root {}",
			want:   "not allowed to extend sealed class",
		},
		{
			name:   "unpermitted interface",
			source: "public sealed interface Root permits Allowed {} final class Allowed implements Root {} final class Other implements Root {}",
			want:   "not allowed to extend sealed class",
		},
		{
			name:   "missing subclass modifier",
			source: "public sealed class Root permits Allowed {} class Allowed extends Root {}",
			want:   "sealed, non-sealed or final modifiers expected",
		},
		{
			name:   "extending final type",
			source: "public final class Root {} class Other extends Root {}",
			want:   "cannot inherit from final",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "Root.java")
			writeTestFile(t, file, "package reference.invalid;\n"+test.source)
			report, err := Analyze(t.Context(), []string{file}, Options{Java: java, Release: release, MethodBodies: true})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := EmitGPPPackages(report); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("emitter error %v, want %q", err, test.want)
			}
		})
	}
}
