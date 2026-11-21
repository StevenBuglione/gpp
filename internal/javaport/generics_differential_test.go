package javaport

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestJavaErasedGenericsMatchOpenJDK(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	sources := []struct{ name, java string }{
		{"Carrier", `package reference.generics.api;
public interface Carrier<T> {
    T get();
    void set(T value);
    default T echo(T value) { return value; }
}`},
		{"Mapper", `package reference.generics.api;
public interface Mapper<T, R> { R apply(T value); }`},
		{"Box", `package reference.generics.model;
import reference.generics.api.Carrier;
public class Box<T> implements Carrier<T> {
    public T value;
    public Box(T initial) { value = initial; }
    public T get() { return value; }
    public void set(T next) { value = next; }
}`},
		{"Derived", `package reference.generics.model;
public class Derived<T> extends Box<T> {
    public Derived(T value) { super(value); }
}`},
		{"NumberBox", `package reference.generics.model;
public class NumberBox<N extends Number> {
    public N value;
    public NumberBox(N start) { value = start; }
    public N get() { return value; }
    public int read() { return value.intValue(); }
}`},
		{"Tools", `package reference.generics.model;
public final class Tools {
    private Tools() {}
    public static <T> T identity(T value) { return value; }
    public static <N extends Number> int number(N value) { return value.intValue(); }
}`},
		{"Calls", `package reference.generics.client;
import reference.generics.api.Carrier;
import reference.generics.api.Mapper;
import reference.generics.model.Box;
import reference.generics.model.Derived;
import reference.generics.model.NumberBox;
import reference.generics.model.Tools;
public class Calls {
    public Box<String> diamond(String value) { return new Box<>(value); }
    public int inferred() { return Tools.identity(127) + Tools.number(Integer.valueOf(5)); }
    public String bridge(Carrier<String> value) { return value.echo(value.get()); }
    public String inherited(Derived<String> value) { return value.get(); }
    public int capture(Box<? extends Number> source, Box<? super Integer> target) {
        target.set(7);
        return source.get().intValue();
    }
    public int bound(NumberBox<Integer> value) { return value.read() + value.get().intValue(); }
    public int mutate(Box<Integer> value) { int before = value.value++; return before * 10 + value.value; }
    public String field(Box<String> box, String value) { return box.value = value; }
    public String[] array(Box<String[]> value) { return value.get(); }
    public Box<String[]> arrayBox() { return new Box<>(new String[]{"s"}); }
    public int polluted() {
        Box<String> typed = new Box<>("typed");
        Box raw = typed;
        raw.set(300);
        typed.get();
        Object method = (typed.get());
        Object field = typed.value;
        int score = ((Integer) method).intValue() + ((Integer) field).intValue();
        if (typed.get() != null && !(typed.get() instanceof String)) score += 100;
        try { Object value = Tools.identity(typed.get()); score -= 10000; }
        catch (ClassCastException failure) { score += 1000; }
        try { new Box<String>(typed.get()); score -= 10000; }
        catch (ClassCastException failure) { score += 2000; }
        try { return typed.get().length(); }
        catch (ClassCastException failure) { score += 1; }
        try { return typed.value.length(); }
        catch (ClassCastException failure) { return score + 10; }
    }
    public int nullBody(Box<String> value) { return value.get() == null ? 1 : 0; }
    public Mapper<String, Integer> mapper() {
        return (var value) -> { try { return value.length(); }
            catch (NullPointerException failure) { return -1; } };
    }
    public Mapper<String, Integer> methodMapper() { return String::length; }
    public Mapper<String, String> genericMethodMapper() { return Tools::identity; }
    public Mapper<String, Box<String>> constructorMapper() { return Box<String>::new; }
    public int apply(Mapper<String, Integer> source) { return source.apply("abcd"); }
    public int badMapper() {
        Mapper raw = mapper();
        try { raw.apply(2); return 0; }
        catch (ClassCastException failure) { return 1; }
    }
}`},
	}
	var inputs []string
	for _, source := range sources {
		path := filepath.Join(root, source.name+".java")
		writeTestFile(t, path, source.java)
		inputs = append(inputs, path)
	}
	probe := filepath.Join(root, "Probe.java")
	writeTestFile(t, probe, `import reference.generics.client.Calls;
import reference.generics.model.*;
class Probe {
    public static void main(String[] args) {
        var calls = new Calls();
        var text = calls.diamond("initial");
        var number = new Box<>(5);
        var output = new Box<Object>(null);
        System.out.println(calls.bridge(text) + " " + calls.inherited(new Derived<>("derived")) + " " + calls.inferred());
        System.out.println(calls.capture(number, output) + " " + output.get() +
            " " + calls.bound(new NumberBox<>(8)) + " " + calls.mutate(number));
        System.out.println(calls.field(text, "next") + " " + text.value + " " + calls.array(calls.arrayBox())[0]);
        System.out.println(calls.polluted() + " " + calls.nullBody(new Box<>(null)));
        System.out.println(calls.mapper().apply(null) + " " + calls.apply(calls.mapper()) + " " +
            calls.apply(calls.methodMapper()) + " " + calls.genericMethodMapper().apply("kept") + " " +
            calls.constructorMapper().apply("constructed").get() + " " + calls.badMapper());
    }
}`)
	classes := filepath.Join(root, "classes")
	javacArgs := append([]string{"--release", strconv.Itoa(release), "-sourcepath", classes, "-d", classes, probe}, inputs...)
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"), javacArgs...)
	want := runObjectTestCommand(t, root, java, "-cp", classes, "Probe")

	report, err := Analyze(t.Context(), inputs, Options{Java: java, Release: release, MethodBodies: true})
	if err != nil {
		t.Fatal(err)
	}
	units, err := EmitGPPPackages(report)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, unit := range units {
		path := filepath.Join(root, unit.Name+".gpp")
		writeTestFile(t, path, unit.Source)
		files = append(files, path)
	}
	output := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions(files, output, compiler.CompileOptions{ModulePath: "example.com/java-generics"}); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(root, "caller")
	if err := os.Mkdir(external, 0755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(external, "go.mod"), `module example.com/generic-consumer

go 1.27

require example.com/java-generics v0.0.0

replace example.com/java-generics => ../generated
`)
	writeTestFile(t, filepath.Join(external, "main.go"), `package main

import (
    "fmt"
    "example.com/java-generics/gpp/java"
    "example.com/java-generics/reference/generics/api"
    "example.com/java-generics/reference/generics/client"
    "example.com/java-generics/reference/generics/model"
)

type nativeCarrier struct { value any }
func (carrier *nativeCarrier) Get() any { return carrier.value }
func (carrier *nativeCarrier) Set(value any) { carrier.value = value }
func (*nativeCarrier) Echo(value any) any { return value }
var _ api.Carrier = (*nativeCarrier)(nil)
var _ api.Carrier = (*model.Box)(nil)
type nativeMapper struct{}
func (nativeMapper) Apply(value any) any {
    size, _ := java.StringLength(value.(*java.String))
    return java.BoxInteger(size)
}
var _ api.Mapper = nativeMapper{}

func asText(value *java.String) string { result, _ := value.ToGo(); return result }

func main() {
    calls := client.NewCalls()
    text := calls.Diamond(java.StringFromGo("initial"))
    number := model.NewBoxWithObject(java.BoxInteger(5))
    output := model.NewBoxWithObject(nil)
    native := &nativeCarrier{value: java.StringFromGo("initial")}
    if asText(calls.Bridge(native)) != asText(calls.Bridge(text)) {
        panic("external Go erased interface differs")
    }
    fmt.Println(asText(calls.Bridge(text)), asText(calls.Inherited(model.NewDerivedWithObject(java.StringFromGo("derived")))), calls.Inferred())
    fmt.Println(calls.Capture(number, output), java.UnboxInteger(output.Get().(*java.Integer)),
        calls.Bound(model.NewNumberBoxWithNumber(java.BoxInteger(8))), calls.Mutate(number))
    fmt.Println(asText(calls.Field(text, java.StringFromGo("next"))), asText(text.GppJavaGetField_Box_value().(*java.String)),
        asText(java.ArrayGet[*java.String](calls.Array(calls.ArrayBox()), 0)))
    fmt.Println(calls.Polluted(), calls.NullBody(model.NewBoxWithObject(nil)))
    if calls.Apply(nativeMapper{}) != calls.Apply(calls.Mapper()) { panic("external generic Go callback differs") }
    fmt.Println(java.UnboxInteger(calls.Mapper().Apply(nil).(*java.Integer)), calls.Apply(calls.Mapper()),
        calls.Apply(calls.MethodMapper()), asText(calls.GenericMethodMapper().Apply(java.StringFromGo("kept")).(*java.String)),
        asText(calls.ConstructorMapper().Apply(java.StringFromGo("constructed")).(*model.Box).Get().(*java.String)), calls.BadMapper())
}
`)
	if got := runObjectTestCommand(t, external, "go", "run", "-buildvcs=false", "-mod=mod", "."); got != want {
		t.Fatalf("GPP erased generics diverged from Java\nGo:\n%s\nOpenJDK:\n%s", got, want)
	}
}
