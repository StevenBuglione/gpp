package javaport

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestNativeGPPInheritedAnnotationMetadataMatchesJava(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	reference := filepath.Join(root, "Probe.java")
	writeTestFile(t, reference, `import java.lang.annotation.*;
import java.lang.reflect.Modifier;

class Probe {
    @Retention(RetentionPolicy.RUNTIME)
    @Target({ElementType.TYPE, ElementType.FIELD, ElementType.METHOD, ElementType.PARAMETER})
    @interface Mark {
        String label();
        int rank() default 2;
    }

    @Mark(label = "ancestor")
    static class Base {
        private static final String FIELD_LABEL = "field";
        @Mark(label = FIELD_LABEL, rank = 5) public int Value = 17;
        @Mark(label = "lookup")
        public String Lookup(@Mark(label = "query", rank = 4) String query) { return query; }
        @Mark(label = "base-only")
        public String Override() { return "base"; }
        @Mark(label = "static")
        public static int Factory(@Mark(label = "seed") int seed) { return seed; }
        @Mark(label = "read-int")
        public int Read(int value) { return value; }
        @Mark(label = "read-string")
        public String Read(String value) { return value; }
    }
    static class Middle extends Base {}
    static class Child extends Middle {
        @Override public String Override() { return "child"; }
    }
    static void print(Class<?> owner, Mark mark) {
        System.out.println(owner.getSimpleName() + " " + mark.label() + " " + mark.rank());
    }
    public static void main(String[] args) throws Exception {
        var type = Child.class;
        var field = type.getField("Value");
        print(field.getDeclaringClass(), field.getAnnotation(Mark.class));
        var lookup = type.getMethod("Lookup", String.class);
        print(lookup.getDeclaringClass(), lookup.getAnnotation(Mark.class));
        print(lookup.getDeclaringClass(), lookup.getParameters()[0].getAnnotation(Mark.class));
        var factory = type.getMethod("Factory", int.class);
        print(factory.getDeclaringClass(), factory.getAnnotation(Mark.class));
        print(factory.getDeclaringClass(), factory.getParameters()[0].getAnnotation(Mark.class));
        System.out.println(Modifier.isStatic(factory.getModifiers()));
        for (var param : new Class<?>[]{int.class, String.class}) {
            var overload = type.getMethod("Read", param);
            print(overload.getDeclaringClass(), overload.getAnnotation(Mark.class));
        }
        var override = type.getMethod("Override");
        System.out.println(override.getDeclaringClass().getSimpleName() + " " + override.isAnnotationPresent(Mark.class));
        System.out.println(type.isAnnotationPresent(Mark.class));
        print(Base.class, Base.class.getAnnotation(Mark.class));
    }
}`)
	classes := filepath.Join(root, "classes")
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"),
		"--release", strconv.Itoa(release), "-d", classes, reference)
	want := runObjectTestCommand(t, root, java, "-cp", classes, "Probe")

	tags := filepath.Join(root, "tags.gpp")
	writeTestFile(t, tags, `package reference.meta.tags

annotation Mark(label any, rank int = 2) on class, field, method, parameter
`)
	base := filepath.Join(root, "base.gpp")
	writeTestFile(t, base, `package reference.inherited.base

import meta reference.meta.tags

const fieldLabel = "field"

class Base @{meta.Mark("ancestor")} {
    Value int = 17 @{meta.Mark(label: fieldLabel, rank: 5)}
    func Lookup(query string @{meta.Mark("query", 4)}) string @{meta.Mark("lookup")} {
        return query
    }
    func Override() string @{meta.Mark("base-only")} { return "base" }
    static func Factory(seed int @{meta.Mark("seed")}) int @{meta.Mark("static")} { return seed }
    func Read(value int) int @{meta.Mark("read-int")} { return value }
    func Read(value string) string @{meta.Mark("read-string")} { return value }
}
`)
	middle := filepath.Join(root, "middle.gpp")
	writeTestFile(t, middle, `package reference.inherited.middle

import core reference.inherited.base

class Middle : core.Base {}
`)
	child := filepath.Join(root, "child.gpp")
	writeTestFile(t, child, `package reference.inherited.child

import bridge reference.inherited.middle

class Child : bridge.Middle {
    func Override() string { return "child" }
}

func NewChild() *Child { return Child() }
`)
	output := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions([]string{tags, base, middle, child}, output,
		compiler.CompileOptions{ModulePath: "example.com/annotation-inheritance"}); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(output, "main.go"), `package main

import (
    "fmt"
    "example.com/annotation-inheritance/gpp/runtime"
    "example.com/annotation-inheritance/reference/inherited/base"
    "example.com/annotation-inheritance/reference/inherited/child"
    "example.com/annotation-inheritance/reference/meta/tags"
)

func method(class *runtime.GppClass, name, parameter string) *runtime.GppMethod {
    for i := range class.Methods {
        candidate := &class.Methods[i]
        if candidate.Name == name &&
            (parameter == "" || len(candidate.Parameters) == 1 && candidate.Parameters[0].Type.Name == parameter) {
            return candidate
        }
    }
    panic("missing method " + name + " " + parameter)
}

func printMark(owner *runtime.GppClass, annotations runtime.GppAnnotations) {
    mark := annotations.Find(tags.GppAnnotation_Mark)
    if mark == nil || mark.Type != tags.GppAnnotation_Mark { panic("annotation declaration identity lost") }
    fmt.Println(owner.Name, mark.Args[0], mark.Args[1])
}

func main() {
    class := child.GppChildClass
    field := &class.Fields[0]
    if field.Name != "Value" || field.Owner != base.GppBaseClass { panic("incorrect field owner") }
    object := child.NewChild()
    if field.Get(object) != 17 || field.Addr(object) != &object.Middle.Base.Value {
        panic("invalid inherited field accessors")
    }
    field.Set(object, 23)
    if object.Middle.Base.Value != 23 { panic("invalid inherited field setter") }
    printMark(field.Owner, field.Annotations)
    lookup := method(class, "Lookup", "string")
    if lookup.Owner != base.GppBaseClass { panic("incorrect method owner") }
    printMark(lookup.Owner, lookup.Annotations)
    printMark(lookup.Owner, lookup.Parameters[0].Annotations)
    factory := method(class, "Factory", "int")
    printMark(factory.Owner, factory.Annotations)
    printMark(factory.Owner, factory.Parameters[0].Annotations)
    fmt.Println(factory.Static)
    for _, parameter := range []string{"int", "string"} {
        overload := method(class, "Read", parameter)
        printMark(overload.Owner, overload.Annotations)
    }
    override := method(class, "Override", "")
    fmt.Println(override.Owner.Name, override.Annotations.Has(tags.GppAnnotation_Mark))
    fmt.Println(class.Annotations.Has(tags.GppAnnotation_Mark))
    printMark(base.GppBaseClass, base.GppBaseClass.Annotations)

    field.Annotations[0].Args[0] = "changed"
    lookup.Annotations[0].Name = "changed"
    lookup.Parameters[0].Name = "changed"
    lookup.Parameters[0].Type.Name = "changed"
    lookup.Parameters[0].Annotations[0].Args[0] = "changed"
    original := method(base.GppBaseClass, "Lookup", "string")
    if base.GppBaseClass.Fields[0].Annotations[0].Args[0] != "field" ||
        original.Annotations[0].Name != "Mark" || original.Parameters[0].Name != "query" ||
        original.Parameters[0].Annotations[0].Args[0] != "query" {
        panic("subclass metadata mutated the declaring class")
    }
}
`)
	got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", ".")
	if got != want {
		t.Fatalf("GPP and Java inherited metadata diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}
