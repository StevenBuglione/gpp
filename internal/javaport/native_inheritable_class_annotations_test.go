package javaport

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestNativeGPPInheritableClassAnnotationsMatchJava(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	reference := filepath.Join(root, "Probe.java")
	writeTestFile(t, reference, `import java.lang.annotation.*;
import java.util.Arrays;
import java.util.stream.Collectors;

class Probe {
    @Inherited @Repeatable(Routes.class) @Retention(RetentionPolicy.RUNTIME)
    @interface Route { String value(); }
    @Inherited @Retention(RetentionPolicy.RUNTIME)
    @interface Routes { Route[] value(); }
    @Retention(RetentionPolicy.RUNTIME)
    @interface Plain {}
    @Inherited @Retention(RetentionPolicy.RUNTIME)
    @interface Filter {}

    static int initialized;
    @Route("first") @Route("second") @Plain @Filter
    static class Base { static { initialized++; } }
    static class Child extends Base {}
    @Route("near")
    static class Override extends Child {}
    static class Leaf extends Override {}
    static class Generic<T> extends Leaf {}
    @Route("interface")
    interface Marker {}
    static class Empty {}
    static class Secondary extends Empty implements Marker {}
    static class Foreign {
        @Inherited @Retention(RetentionPolicy.RUNTIME)
        @interface Route {}
    }

    static String routes(Route[] routes) {
        return Arrays.stream(routes).map(Route::value).collect(Collectors.joining(","));
    }
    public static void main(String[] args) {
        for (Class<?> type : new Class<?>[]{Base.class, Child.class, Override.class, Leaf.class, Generic.class, Secondary.class}) {
            System.out.println(type.getSimpleName() + " " +
                routes(type.getAnnotationsByType(Route.class)) + "/" +
                routes(type.getDeclaredAnnotationsByType(Route.class)) + " " +
                type.getAnnotationsByType(Plain.class).length + " " +
                type.getAnnotationsByType(Filter.class).length + " " +
                type.getAnnotationsByType(Foreign.Route.class).length);
        }
        System.out.println(routes(Leaf.class.getAnnotationsByType(Route.class)));
        System.out.println(routes(Child.class.getDeclaredAnnotationsByType(Route.class)));
        System.out.println(initialized);
    }
}
`)
	classes := filepath.Join(root, "classes")
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"),
		"--release", strconv.Itoa(release), "-d", classes, reference)
	want := runObjectTestCommand(t, root, java, "-cp", classes, "Probe")

	tags := filepath.Join(root, "tags.gpp")
	writeTestFile(t, tags, `package reference.inherited.tags

annotation (
    Route(value string) inherited on class
    Plain on class
    Filter inherited on class
)
`)
	foreign := filepath.Join(root, "foreign.gpp")
	writeTestFile(t, foreign, `package reference.inherited.foreign

annotation Route inherited
`)
	base := filepath.Join(root, "base.gpp")
	writeTestFile(t, base, `package reference.inherited.base

import tags reference.inherited.tags

var Initialized int

class Base @{tags.Route("first"), tags.Route("second"), tags.Plain, tags.Filter} {
    static { Initialized++ }
}
class Marker @{tags.Route("interface")} {}
class Empty {}
`)
	children := filepath.Join(root, "children.gpp")
	writeTestFile(t, children, `package reference.inherited.children

import root reference.inherited.base
import rules reference.inherited.tags

class Child : root.Base {}
class Override : Child @{rules.Route("near")} {}
class Leaf : Override {}
class Generic[T any] : Leaf {}
class Secondary : root.Empty, root.Marker {}

func LeafRoutes() GppAnnotations {
    return Leaf.class.getAnnotationsByType(rules.Route)
}

func ChildDeclarations() GppAnnotations {
    return Child.class.getDeclaredAnnotationsByType(rules.Route)
}
`)
	output := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions([]string{tags, foreign, base, children}, output, compiler.CompileOptions{
		ModulePath: "example.com/inherited-annotations",
	}); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(output, "main.go"), `package main

import (
    "fmt"
    "strings"

    "example.com/inherited-annotations/reference/inherited/base"
    "example.com/inherited-annotations/reference/inherited/children"
    "example.com/inherited-annotations/reference/inherited/foreign"
    "example.com/inherited-annotations/reference/inherited/tags"
)

func routes(values base.GppAnnotations) string {
    names := make([]string, len(values))
    for index, value := range values {
        names[index] = value.Args[0].(string)
    }
    return strings.Join(names, ",")
}

func main() {
    route := tags.GppAnnotation_Route
    plain := tags.GppAnnotation_Plain
    if !route.IsInherited() || plain.IsInherited() || !foreign.GppAnnotation_Route.IsInherited() {
        panic("annotation descriptors lost inheritance metadata")
    }
    for _, class := range []*base.GppClass{base.GppBaseClass, children.GppChildClass, children.GppOverrideClass,
            children.GppLeafClass, children.GppGenericClass, children.GppSecondaryClass} {
        fmt.Printf("%s %s/%s %d %d %d\n", class.Name,
            routes(class.GetAnnotationsByType(route)), routes(class.GetDeclaredAnnotationsByType(route)),
            len(class.GetAnnotationsByType(plain)), len(class.GetAnnotationsByType(tags.GppAnnotation_Filter)),
            len(class.GetAnnotationsByType(foreign.GppAnnotation_Route)))
    }
    fmt.Println(routes(children.LeafRoutes()))
    fmt.Println(routes(children.ChildDeclarations()))
    fmt.Println(base.Initialized)

    inherited := children.GppChildClass.GetAnnotationsByType(route)
    inherited[0].Args[0] = "changed"
    inherited[1].Name = "changed"
    if routes(base.GppBaseClass.GetAnnotationsByType(route)) != "first,second" ||
        children.GppChildClass.Annotations.Has(route) || base.GppBaseClass.Annotations[1].Name != "Route" {
        panic("annotation queries mutated declaration metadata")
    }
}
`)
	got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", ".")
	if got != want {
		t.Fatalf("GPP and Java inherited class annotation queries diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}
