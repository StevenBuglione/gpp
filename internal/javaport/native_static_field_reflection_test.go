package javaport

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestNativeGPPStaticFieldReflectionMatchesJava(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	reference := filepath.Join(root, "Probe.java")
	writeTestFile(t, reference, `import java.lang.annotation.*;
import java.lang.reflect.*;
import java.util.Arrays;
import java.util.stream.Collectors;

class Probe {
    @Retention(RetentionPolicy.RUNTIME)
    @Target(ElementType.FIELD)
    @interface Mark { String label(); int rank() default 2; }

    static String trace = "";
    static int record(String event, int value) { trace += event; return value; }

    public static class Base {
        static {
            if (Arrays.stream(Base.class.getDeclaredFields()).noneMatch(field -> field.getName().equals("Magic"))) {
                throw new AssertionError("static field metadata hidden during initialization");
            }
            record("B", 0);
        }
        @Mark(label = "count") public static int Count = 7;
        @Mark(label = "constant", rank = 5) public static final int Magic = 29;
        @Mark(label = "computed") public static final int Frozen = record("F", 40);
        @Mark(label = "base hidden") public static int Hidden = 10;
        @Mark(label = "private") private static int secret = 1;
        protected static int _sheltered = 2;
        public int Instance = 3;
    }
    public static class Middle extends Base {}
    public static class Leaf extends Middle {
        static { record("L", 0); }
        @Mark(label = "leaf hidden") public static int Hidden = 99;
    }
    public static class WrongType {
        static { record("T", 0); }
        public static int Value = 5;
    }
    public static class WrongFinal {
        static { record("R", 0); }
        public static final int Value = 6;
    }

    static String staticNames(Field[] fields) {
        return Arrays.stream(fields)
                .filter(field -> Modifier.isStatic(field.getModifiers()))
                .map(field -> field.getDeclaringClass().getSimpleName() + "." + field.getName())
                .sorted().collect(Collectors.joining(","));
    }
    static void describe(Field field) {
        var mark = field.getAnnotation(Mark.class);
        System.out.println(field.getDeclaringClass().getSimpleName() + " " + field.getName() +
                " " + field.getType().getTypeName() + " " + Modifier.isFinal(field.getModifiers()) +
                " " + Modifier.isPublic(field.getModifiers()) + " " + mark.label() + " " + mark.rank());
    }
    static boolean rejects(Field field, Object value) {
        try {
            field.set(null, value);
            return false;
        } catch (IllegalAccessException | IllegalArgumentException expected) {
            return true;
        }
    }
    public static void main(String[] args) throws Exception {
        var leaf = Leaf.class;
        var magic = leaf.getField("Magic");
        var count = leaf.getField("Count");
        var hidden = leaf.getField("Hidden");
        var frozen = leaf.getField("Frozen");
        System.out.println("declared=" + staticNames(Base.class.getDeclaredFields()));
        System.out.println("public=" + staticNames(leaf.getFields()));
        describe(magic);
        describe(count);
        describe(hidden);
        describe(Base.class.getDeclaredField("secret"));
        System.out.println("direct " + Leaf.Magic + " trace=" + trace);
        System.out.println("reflect " + magic.get(null) + " trace=" + trace);
        hidden.set(null, 88);
        count.set(null, 42);
        System.out.println("write " + hidden.get(null) + " " + count.get(null) + " trace=" + trace);
        System.out.println("rejected " + rejects(magic, 17) + " " + rejects(frozen, 17) +
                " " + rejects(count, "wrong") + " " + frozen.get(null) + " " + count.get(null));
        System.out.println("reject-type " + rejects(WrongType.class.getField("Value"), "wrong") + " trace=" + trace);
        System.out.println("reject-final " + rejects(WrongFinal.class.getField("Value"), 17) + " trace=" + trace);
    }
}`)
	classes := filepath.Join(root, "classes")
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"),
		"--release", strconv.Itoa(release), "-d", classes, reference)
	want := runObjectTestCommand(t, root, java, "-cp", classes, "Probe")

	tags := filepath.Join(root, "tags.gpp")
	writeTestFile(t, tags, `package reference.staticmeta.tags

annotation Mark(label string, rank int = 2) on field
`)
	base := filepath.Join(root, "base.gpp")
	writeTestFile(t, base, `package reference.staticmeta.base

import meta reference.staticmeta.tags

var Trace string

func Record(event string, value int) int {
    Trace += event
    return value
}

class Base {
    static {
        constMetadata := Base.class.DeclaredStaticField("Magic")
        if constMetadata == nil || constMetadata.Name != "Magic" {
            panic("static field metadata hidden during initialization")
        }
        Record("B", 0)
    }
    static Count int = 7 @{meta.Mark("count")}
    static const Magic int = 29 @{meta.Mark(label: "constant", rank: 5)}
    static final Frozen int = Record("F", 40) @{meta.Mark("computed")}
    static Hidden int = 10 @{meta.Mark("base hidden")}
    static secret int = 1 @{meta.Mark("private")}
    static _sheltered int = 2
    Instance int = 3
}
`)
	middle := filepath.Join(root, "middle.gpp")
	writeTestFile(t, middle, `package reference.staticmeta.middle

import core reference.staticmeta.base

class Middle : core.Base {}
`)
	leaf := filepath.Join(root, "leaf.gpp")
	writeTestFile(t, leaf, `package reference.staticmeta.leaf

import reference.staticmeta.base
import bridge reference.staticmeta.middle
import meta reference.staticmeta.tags

var Early = Leaf.class.PublicStaticField("Magic")

class Leaf : bridge.Middle {
    static { base.Record("L", 0) }
    static Hidden int = 99 @{meta.Mark("leaf hidden")}
}
class WrongType {
    static { base.Record("T", 0) }
    static Value int = 5
}
class WrongFinal {
    static { base.Record("R", 0) }
    static const Value int = 6
}

func FieldName() string { return Leaf.class.publicStaticField("Magic").name }
func Constant() int { return Leaf.Magic }
`)
	output := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions([]string{tags, base, middle, leaf}, output,
		compiler.CompileOptions{ModulePath: "example.com/static-field-reflection"}); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(output, "main.go"), `package main

import (
    "fmt"
    "sort"
    "strings"

    "example.com/static-field-reflection/gpp/runtime"
    "example.com/static-field-reflection/reference/staticmeta/base"
    "example.com/static-field-reflection/reference/staticmeta/leaf"
    "example.com/static-field-reflection/reference/staticmeta/tags"
)

func staticNames(fields []runtime.GppStaticField) string {
    names := make([]string, 0, len(fields))
    for _, field := range fields {
        names = append(names, field.Owner.Name + "." + field.Name)
    }
    sort.Strings(names)
    return strings.Join(names, ",")
}

func describe(field *runtime.GppStaticField) {
    mark := field.Annotations.Find(tags.GppAnnotation_Mark)
    if mark == nil || mark.Type != tags.GppAnnotation_Mark {
        panic("annotation declaration identity lost")
    }
    fmt.Println(field.Owner.Name, field.Name, field.Type.Name, field.Final, field.Public, mark.Args[0], mark.Args[1])
}

func main() {
    class := leaf.GppLeafClass
    magic := class.PublicStaticField("Magic")
    count := class.PublicStaticField("Count")
    hidden := class.PublicStaticField("Hidden")
    frozen := class.PublicStaticField("Frozen")
    secret := base.GppBaseClass.DeclaredStaticField("secret")
    if magic == nil || count == nil || hidden == nil || frozen == nil || secret == nil {
        panic("static field metadata missing")
    }
    if leaf.Early == nil || leaf.Early.Owner != base.GppBaseClass ||
        leaf.FieldName() != "Magic" || magic.Owner != base.GppBaseClass ||
        hidden.Owner != class || count.Owner != base.GppBaseClass ||
        class.DeclaredStaticField("Magic") != nil || class.PublicStaticField("secret") != nil {
        panic("declared and inherited field lookup diverged")
    }
    if !magic.Constant || !magic.Final || frozen.Constant || !frozen.Final || count.Final {
        panic("static field modifiers diverged")
    }
    if len(base.GppBaseClass.Fields) != 1 || base.GppBaseClass.Fields[0].Name != "Instance" {
        panic("static fields altered instance descriptors")
    }
    fmt.Println("declared=" + staticNames(base.GppBaseClass.DeclaredStaticFields()))
    fmt.Println("public=" + staticNames(class.PublicStaticFields()))
    describe(magic)
    describe(count)
    describe(hidden)
    describe(secret)

    magic.Type.Name = "mutated"
    magic.Annotations[0].Args[0] = "mutated"
    fields := class.PublicStaticFields()
    fields[0].Name = "mutated"
    original := base.GppBaseClass.DeclaredStaticField("Magic")
    if original == nil || original.Type.Name != "int" || original.Annotations[0].Args[0] != "constant" ||
        class.PublicStaticField("Magic").Type.Name != "int" ||
        class.PublicStaticField("Magic").Annotations[0].Args[0] != "constant" {
        panic("returned metadata shares writable descriptor storage")
    }
    fmt.Println("direct", leaf.Constant(), "trace=" + base.Trace)
    fmt.Println("reflect", magic.Get(), "trace=" + base.Trace)
    if err := hidden.Set(88); err != nil { panic(err) }
    if err := count.Set(42); err != nil { panic(err) }
    fmt.Println("write", hidden.Get(), count.Get(), "trace=" + base.Trace)
    fmt.Println("rejected", magic.Set(17) != nil, frozen.Set(17) != nil,
        count.Set("wrong") != nil, frozen.Get(), count.Get())
    fmt.Println("reject-type", leaf.GppWrongTypeClass.PublicStaticField("Value").Set("wrong") != nil, "trace=" + base.Trace)
    fmt.Println("reject-final", leaf.GppWrongFinalClass.PublicStaticField("Value").Set(17) != nil, "trace=" + base.Trace)
}
`)
	got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", ".")
	if got != want {
		t.Fatalf("GPP and Java static field reflection diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}

func TestNativeGPPGenericStaticFieldMetadataPreservesGoLayout(t *testing.T) {
	root := t.TempDir()
	box := filepath.Join(root, "box.gpp")
	writeTestFile(t, box, `package reference.staticmeta.box

class Box[T any] {
    static const Limit int = 11
    Value T
}
`)
	datum := filepath.Join(root, "plain.gpp")
	writeTestFile(t, datum, `package reference.staticmeta.plain

class Datum { Value int }

type Entries map[string]int

class Handles {
    static Config Entries = Entries{"value": 5}
}
`)
	output := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions([]string{box, datum}, output,
		compiler.CompileOptions{ModulePath: "example.com/static-field-layout"}); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(output, "main.go"), `package main

import (
    "example.com/static-field-layout/gpp/runtime"
    "example.com/static-field-layout/reference/staticmeta/box"
    "example.com/static-field-layout/reference/staticmeta/plain"
)

func main() {
    datum := plain.Datum{31}
    value := box.Box[string]{"text"}
    var class *runtime.GppClass = box.GppBoxClass
    field := class.DeclaredStaticField("Limit")
    if datum.Value != 31 || value.Value != "text" ||
        field == nil || field.Owner != class || field.Get() != 11 ||
        runtime.ClassByName(class.Package + "." + class.Name) != class {
        panic("generic static metadata changed Go layouts or class identity")
    }
    config := plain.GppHandlesClass.DeclaredStaticField("Config")
    if config.Get().(plain.Entries)["value"] != 5 {
        panic("typed Go map field could not be read")
    }
    if err := config.Set(nil); err != nil || config.Get().(plain.Entries) != nil {
        panic("typed Go map field did not accept nil")
    }
}
`)
	runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", ".")
}

func TestNativeGPPStaticFieldAnnotationChecksTarget(t *testing.T) {
	file, err := compiler.ParseFile("probe.gpp", `package main
annotation ClassOnly on class
class Probe {
    static const Limit int = 11 @{ClassOnly}
}
`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = compiler.Emit(file)
	if err == nil || !strings.Contains(err.Error(), "ClassOnly") || !strings.Contains(err.Error(), "field") {
		t.Fatalf("expected a field annotation target error, got %v", err)
	}
}
