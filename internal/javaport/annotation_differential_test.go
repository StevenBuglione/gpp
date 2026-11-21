package javaport

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestJavaAOTAnnotationsMatchOpenJDK(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	javaFiles := []struct{ pkg, name, body string }{
		{"rules", "Root", `@Retention(RetentionPolicy.RUNTIME) @Target(ElementType.ANNOTATION_TYPE)
public @interface Root {}`},
		{"rules", "Tag", `import reference.annotations.model.Local;
import reference.annotations.model.Model;
@Local @Retention(RetentionPolicy.RUNTIME) @Inherited @Repeatable(Tags.class)
public @interface Tag {
    String value();
    int weight() default 7;
    int[] flags() default {1, 2};
    Class<?> owner() default Model.class;
    RetentionPolicy policy() default RetentionPolicy.CLASS;
    Flavor flavor() default @Flavor(data = "\uD800");
    double marker() default -0.0;
}`},
		{"rules", "Tags", `@Retention(RetentionPolicy.RUNTIME) @Inherited
public @interface Tags { Tag[] value(); }`},
		{"rules", "Flavor", `public @interface Flavor { String data(); }`},
		{"rules", "DefaultOnly", `public @interface DefaultOnly {}`},
		{"rules", "SourceOnly", `@Retention(RetentionPolicy.SOURCE) public @interface SourceOnly {}`},
		{"model", "Local", `@Root @Target({ElementType.TYPE, ElementType.ANNOTATION_TYPE})
@Retention(RetentionPolicy.RUNTIME) public @interface Local {}`},
		{"model", "Model", `@Tag("one") @Tag(value = "two", weight = 4) @DefaultOnly @SourceOnly
public class Model {
    @Tag("field") public int level;
    public Model() {}
    @Tag("ctor") public Model(@Tag("arg") int initial) { level = initial; }
    @Tag("method") public int work(int ignored, @Tag("param") int value) { return level + value; }
    @Tag("erased") public <T> T keep(@Tag("generic") T value) { return value; }
}`},
		{"model", "Child", `public class Child extends Model {}`},
		{"model", "Variant", `@Tag("child") public class Variant extends Child {}`},
		{"model", "Explicit", `@Tags({@Tag("explicit")}) public class Explicit extends Model {}`},
		{"model", "Hint", `@Tag("iface") public interface Hint { @Tag("method") int code(@Tag("arg") int n); }`},
		{"model", "Impl", `@Local public class Impl implements Hint { public int code(int n) { return n; } }`},
		{"records", "Data", `import reference.annotations.rules.Tag;
@Tag("record") public record Data(int value) {}`},
	}
	var inputs []string
	for _, file := range javaFiles {
		path := filepath.Join(root, file.name+".java")
		header := "package reference.annotations." + file.pkg + ";\nimport java.lang.annotation.*;\n"
		if file.pkg == "model" {
			header += "import reference.annotations.rules.*;\n"
		}
		writeTestFile(t, path, header+file.body)
		inputs = append(inputs, path)
	}
	probe := filepath.Join(root, "Probe.java")
	writeTestFile(t, probe, `import java.util.Arrays;
import java.util.stream.Collectors;
import java.lang.annotation.Retention;
import reference.annotations.model.*;
import reference.annotations.records.Data;
import reference.annotations.rules.*;
class Probe {
    static String tags(Tag[] values) {
        return Arrays.stream(values).map(tag -> tag.value() + ":" + tag.weight()).collect(Collectors.joining(","));
    }
    public static void main(String[] args) throws Exception {
        Tag first = Model.class.getDeclaredAnnotationsByType(Tag.class)[0];
        System.out.printf("%d %b %b %b %b%n", Model.class.getDeclaredAnnotations().length,
            Model.class.getDeclaredAnnotation(Tag.class) == null, Model.class.getDeclaredAnnotation(Tags.class) != null,
            Model.class.getDeclaredAnnotation(DefaultOnly.class) == null, Model.class.getDeclaredAnnotation(SourceOnly.class) == null);
        System.out.println(tags(Model.class.getDeclaredAnnotationsByType(Tag.class)));
        System.out.println(tags(Child.class.getAnnotationsByType(Tag.class)) + " " + (Child.class.getAnnotation(Tag.class) == null));
        System.out.println(tags(Variant.class.getAnnotationsByType(Tag.class)) + " " + tags(Explicit.class.getDeclaredAnnotationsByType(Tag.class)));
        int[] flags = first.flags();
        flags[0] = 99;
        System.out.printf("%s %s %s %04x %016x%n", Arrays.toString(first.flags()), first.owner().getName(),
            first.policy().name(), (int) first.flavor().data().charAt(0), Double.doubleToRawLongBits(first.marker()));
        System.out.println(Tag.class.getDeclaredMethod("value").getDefaultValue() == null);
        System.out.println(Tag.class.getDeclaredMethod("weight").getDefaultValue());
        System.out.printf("%s %s %s %s %s %b%n", Model.class.getDeclaredField("level").getAnnotation(Tag.class).value(),
            Model.class.getDeclaredConstructor(int.class).getAnnotation(Tag.class).value(),
            Model.class.getDeclaredConstructor(int.class).getParameters()[0].getAnnotation(Tag.class).value(),
            Model.class.getDeclaredMethod("work", int.class, int.class).getAnnotation(Tag.class).value(),
            Model.class.getDeclaredMethod("work", int.class, int.class).getParameters()[1].getAnnotation(Tag.class).value(),
            Model.class.getDeclaredMethod("work", int.class, int.class).getParameters()[0].getAnnotation(Tag.class) == null);
        System.out.println(Hint.class.getDeclaredMethod("code", int.class).getParameters()[0].getAnnotation(Tag.class).value()
            + " " + (Impl.class.getAnnotation(Tag.class) == null));
        var generic = Model.class.getDeclaredMethod("keep", Object.class);
        System.out.println(generic.getAnnotation(Tag.class).value() + " " +
            generic.getParameters()[0].getAnnotation(Tag.class).value() + " " + new Model().keep("value"));
        System.out.println(Variant.class.getAnnotation(Tag.class).annotationType().getAnnotation(Local.class)
            .annotationType().isAnnotationPresent(Root.class) + " " + Tag.class.getAnnotation(Retention.class).value().name());
        System.out.println(new Model(3).work(0, 2));
        System.out.println(Data.class.getAnnotation(Tag.class).value());
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
	foundImplementation := false
	for _, unit := range units {
		foundImplementation = foundImplementation || strings.HasPrefix(unit.Name, "internal.gppjavaport.")
		file := filepath.Join(root, unit.Name+".gpp")
		writeTestFile(t, file, unit.Source)
		sources = append(sources, file)
	}
	if !foundImplementation {
		t.Fatal("meta-annotation package cycle was not lowered")
	}
	generated := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions(sources, generated, compiler.CompileOptions{ModulePath: "example.com/java-annotations"}); err != nil {
		t.Fatal(err)
	}
	caller := filepath.Join(root, "consumer")
	if err := os.Mkdir(caller, 0755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(caller, "go.mod"), `module example.com/annotation-consumer

go 1.27

require example.com/java-annotations v0.0.0

replace example.com/java-annotations => ../generated
`)
	writeTestFile(t, filepath.Join(caller, "main.go"), `package main

import (
    "fmt"
    "math"
    "strings"
    java "example.com/java-annotations/gpp/java"
    "example.com/java-annotations/reference/annotations/model"
    "example.com/java-annotations/reference/annotations/records"
    "example.com/java-annotations/reference/annotations/rules"
)

func name(annotation java.Annotation) string {
    member, _ := annotation.Member("value")
    text, _ := member.Value().(*java.String).ToGo()
    return text
}

func tags(values []java.Annotation) string {
    texts := make([]string, 0, len(values))
    for _, annotation := range values {
        weight, _ := annotation.Member("weight")
        texts = append(texts, fmt.Sprintf("%s:%d", name(annotation), weight.Value()))
    }
    return strings.Join(texts, ",")
}

func annotationName(element java.AnnotatedElement, kind java.AnnotationType) string {
    annotation, _ := element.GetDeclaredAnnotation(kind)
    return name(annotation)
}

func main() {
    tag := rules.GppJavaAnnotationType_Tag()
    modelType := model.GppJavaAnnotations_Model()
    childType := model.GppJavaAnnotations_Child()
    variantType := model.GppJavaAnnotations_Variant()
    direct := modelType.GetDeclaredAnnotations()
    _, foundTag := modelType.GetDeclaredAnnotation(tag)
    _, foundContainer := modelType.GetDeclaredAnnotation(rules.GppJavaAnnotationType_Tags())
    _, foundClass := modelType.GetDeclaredAnnotation(rules.GppJavaAnnotationType_DefaultOnly())
    _, foundSource := modelType.GetDeclaredAnnotation(rules.GppJavaAnnotationType_SourceOnly())
    fmt.Println(len(direct), !foundTag, foundContainer, !foundClass, !foundSource)
    fmt.Println(tags(modelType.GetDeclaredAnnotationsByType(tag)))
    _, childDirect := childType.GetAnnotation(tag)
    fmt.Println(tags(childType.GetAnnotationsByType(tag)), !childDirect)
    fmt.Println(tags(variantType.GetAnnotationsByType(tag)), tags(model.GppJavaAnnotations_Explicit().GetDeclaredAnnotationsByType(tag)))
    first := modelType.GetDeclaredAnnotationsByType(tag)[0]
    flags, _ := first.Member("flags")
    flags.Value().([]any)[0] = int32(99)
    items := flags.Value().([]any)
    owner, _ := first.Member("owner")
    policy, _ := first.Member("policy")
    flavor, _ := first.Member("flavor")
    data, _ := flavor.Value().(java.Annotation).Member("data")
    marker, _ := first.Member("marker")
    fmt.Printf("[%d, %d] %s %s %04x %016x\n", items[0], items[1], owner.Value().(java.AnnotationClassLiteral),
        policy.Value().(java.AnnotationEnumConstant).Name, data.Value().(*java.String).UTF16()[0], math.Float64bits(marker.Value().(float64)))

    second := modelType.GetDeclaredAnnotationsByType(tag)[1]
    explicitWeight, _ := second.Member("weight")
    defaultWeight, _ := first.Member("weight")
    if !explicitWeight.Explicit() || defaultWeight.Explicit() || defaultWeight.JavaType() != "int" { panic("lost member attributes") }
    for _, member := range tag.Members() {
        if member.Name() == "value" { fmt.Println(!member.HasValue()) }
    }
    for _, member := range tag.Members() {
        if member.Name() == "weight" { fmt.Println(member.Value()) }
    }
    field, _ := modelType.Member("reference.annotations.model.Model#level")
    ctor, _ := modelType.Member("reference.annotations.model.Model#<init>(int)")
    method, _ := modelType.Member("reference.annotations.model.Model#work(int,int)")
    parameters := method.Parameters()
    _, firstParamTagged := parameters[0].GetDeclaredAnnotation(tag)
    fmt.Println(annotationName(field.Annotations(), tag), annotationName(ctor.Annotations(), tag),
        annotationName(ctor.Parameters()[0], tag), annotationName(method.Annotations(), tag),
        annotationName(parameters[1], tag), !firstParamTagged)
    hint, _ := model.GppJavaAnnotations_Hint().Member("reference.annotations.model.Hint#code(int)")
    _, inheritedFromInterface := model.GppJavaAnnotations_Impl().GetAnnotation(tag)
    fmt.Println(annotationName(hint.Parameters()[0], tag), !inheritedFromInterface)
    generic, _ := modelType.Member("reference.annotations.model.Model#keep(java.lang.Object)")
    genericText, _ := model.NewModel().Keep(java.StringFromGo("value")).(*java.String).ToGo()
    fmt.Println(annotationName(generic.Annotations(), tag), annotationName(generic.Parameters()[0], tag), genericText)
    _, foundRoot := variantType.FindMetaAnnotation("reference.annotations.rules.Root")
    fmt.Println(foundRoot, tag.Retention())
    fmt.Println(model.NewModelWithInt(3).Work(0, 2))
    fmt.Println(annotationName(records.GppJavaAnnotations_Data(), tag))
}
`)
	if got := runObjectTestCommand(t, caller, "go", "run", "-mod=mod", "-buildvcs=false", "."); got != want {
		t.Fatalf("GPP and Java annotation metadata diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}
