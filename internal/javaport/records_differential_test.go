package javaport

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestJavaAOTRecordsMatchOpenJDK(t *testing.T) {
	java, release := referenceJDK(t)
	if release < 16 {
		t.Skip("records require Java 16 or newer")
	}
	root := t.TempDir()
	sources := []struct{ file, text string }{
		{"Reading.java", `package reference.records.api;
public interface Reading {
    int ticks();
    String note();
    default String formatted() { return note(); }
}`},
		{"Point.java", `package reference.records.model;
import reference.records.api.Reading;
public record Point(int ticks, double value, String note) implements Reading {
    public static final int identifier;
    static { identifier = 27; }
    public static Point from(Object value) {
        if (value instanceof Point point) return point;
        return new Point(-1, 0.0, null);
    }
}`},
		{"Numeric.java", `package reference.records.model;
public record Numeric(boolean flag, byte small, short medium, char unit, int count,
                      long big, float fraction, double measure, Integer boxed) {}`},
		{"Rate.java", `package reference.records.model;
public record Rate(int fastest, int slowest) {
    public Rate {
        fastest += 2;
        if (slowest < 0) throw new IllegalArgumentException("negative");
    }
    public Rate(int value) { this(value, 3); }
    public int slowest() { return this.slowest + 7; }
}`},
		{"Explicit.java", `package reference.records.model;
public record Explicit(int value) {
    public Explicit(int value) { this.value = value * 3; }
    public int hashCode() { return value + 100; }
}`},
		{"Unit.java", "package reference.records.model; public record Unit() {}"},
		{"Envelope.java", "package reference.records.model; public record Envelope(Record value, Rate rate) {}"},
		{"Buffer.java", "package reference.records.model; public record Buffer(int[] data) {}"},
		{"Catalog.java", `package reference.records.model;
import reference.records.api.Reading;
public final class Catalog {
    public Catalog() {}
    public String fromInterface(Reading value) { return value.formatted(); }
    public Record asRecord(Point value) { return value; }
    public String fromRecord(Record value) { return value.toString(); }
    public Point copy(Point point) { return new Point(point.ticks(), point.value(), point.note()); }
    public boolean same(Point point, Object value) { return point.equals(value); }
    public boolean isRecord(Object value) { return value instanceof Record; }
    public int rejectInvalidRate() {
        try {
            Rate rate = new Rate(1, -1);
            return rate.slowest();
        } catch (IllegalArgumentException failure) {
            return 1;
        }
    }
}`},
	}
	paths := make([]string, 0, len(sources))
	for _, source := range sources {
		path := filepath.Join(root, source.file)
		writeTestFile(t, path, source.text)
		paths = append(paths, path)
	}
	driver := filepath.Join(root, "Driver.java")
	writeTestFile(t, driver, `import reference.records.model.*;

class Driver {
    static String units(String text) {
        StringBuilder result = new StringBuilder();
        for (int index = 0; index < text.length(); index++) result.append(String.format("%04x", (int) text.charAt(index)));
        return result.toString();
    }
    public static void main(String[] args) {
        var catalog = new Catalog();
        var point = new Point(12, 2.5, new String("sample"));
        var copy = catalog.copy(point);
        System.out.printf("%d %s %b %b %b %d%n", point.ticks(), catalog.fromInterface(point),
            catalog.same(point, copy), point == copy, catalog.same(point, new Rate(12)), point.hashCode());
        System.out.printf("%b %b %b %s%n", catalog.isRecord(null), catalog.isRecord(point), catalog.asRecord(null) == null, catalog.fromRecord(catalog.asRecord(point)));
        System.out.printf("%d %b %d%n", Point.identifier, Point.from(point) == point, Point.from(null).ticks());
        var escaped = new Point(-2, -0.0, "\uD800");
        System.out.println(units(escaped.toString()));
        System.out.println(new Point(0, 0.0, null));
        var a = new Numeric(true, (byte) -31, (short) -310, 'Ω', -123, -12345678901234L,
            Float.intBitsToFloat(0x7fc01234), -0.0, Integer.valueOf(500));
        var b = new Numeric(true, (byte) -31, (short) -310, 'Ω', -123, -12345678901234L,
            Float.intBitsToFloat(0x7fc01235), -0.0, Integer.valueOf(500));
        var c = new Numeric(true, (byte) -31, (short) -310, 'Ω', -123, -12345678901234L,
            Float.intBitsToFloat(0x7fc01235), 0.0, Integer.valueOf(500));
        System.out.printf("%b %b %b %d %s%n", a.equals(b), a.equals(c), a.hashCode() == b.hashCode(), a.hashCode(), a);
        var rate = new Rate(4);
        System.out.printf("%d %d %d %s %d%n", rate.fastest(), rate.slowest(), rate.hashCode(), rate, catalog.rejectInvalidRate());
        System.out.printf("%d %d%n", new Explicit(7).value(), new Explicit(7).hashCode());
        var unit = new Unit();
        var other = new Unit();
        System.out.printf("%b %b %d %s%n", unit.equals(other), unit == other, unit.hashCode(), unit);
        var envelope = new Envelope(point, rate);
        System.out.printf("%b %d %s%n", envelope.equals(new Envelope(copy, new Rate(4))), envelope.hashCode(), envelope);
        var buffer = new Buffer(new int[] { 4 });
        var alias = new Buffer(buffer.data());
        var distinct = new Buffer(new int[] { 4 });
        System.out.printf("%b %b %b %b%n", buffer.equals(alias), buffer.equals(distinct), buffer.hashCode() == alias.hashCode(),
            buffer.toString().equals("Buffer[data=[I@" + Integer.toUnsignedString(buffer.hashCode(), 16) + "]"));
    }
}`)
	javaClasses := filepath.Join(root, "classes")
	javacArgs := append([]string{"--release", strconv.Itoa(release), "-sourcepath", javaClasses, "-d", javaClasses, driver}, paths...)
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"), javacArgs...)
	want := runObjectTestCommand(t, root, java, "-cp", javaClasses, "Driver")

	report, err := Analyze(t.Context(), paths, Options{Java: java, Release: release, MethodBodies: true})
	if err != nil {
		t.Fatal(err)
	}
	units, err := EmitGPPPackages(report)
	if err != nil {
		t.Fatal(err)
	}
	inputs := make([]string, 0, len(units))
	for index, unit := range units {
		path := filepath.Join(root, strconv.Itoa(index)+".gpp")
		writeTestFile(t, path, unit.Source)
		inputs = append(inputs, path)
	}
	library := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions(inputs, library, compiler.CompileOptions{
		ModulePath: "example.com/java-records",
	}); err != nil {
		t.Fatal(err)
	}
	caller := t.TempDir()
	writeTestFile(t, filepath.Join(caller, "go.mod"), "module example.com/record-consumer\n\ngo 1.27\n\nrequire example.com/java-records v0.0.0\nreplace example.com/java-records => "+strconv.Quote(filepath.ToSlash(library))+"\n")
	writeTestFile(t, filepath.Join(caller, "main.go"), `package main

import (
    "fmt"
    "math"
    "strings"
    "example.com/java-records/gpp/java"
    "example.com/java-records/reference/records/api"
    "example.com/java-records/reference/records/model"
)

var _ api.Reading = (*model.Point)(nil)
var _ java.Record = (*model.Point)(nil)

func text(value *java.String) string {
    result, _ := value.ToGo()
    return result
}
func units(value *java.String) string {
    var result strings.Builder
    for _, unit := range value.UTF16() { fmt.Fprintf(&result, "%04x", unit) }
    return result.String()
}

func main() {
    catalog := model.NewCatalog()
    point := model.NewPointWithIntAndDoubleAndString(12, 2.5, java.StringFromGo("sample"))
    copy := catalog.Copy(point)
    fmt.Printf("%d %s %t %t %t %d\n", point.Ticks(), text(catalog.FromInterface(point)),
        catalog.Same(point, copy), point == copy, catalog.Same(point, model.NewRateWithInt(12)), point.HashCode())
    fmt.Printf("%t %t %t %s\n", catalog.IsRecord(nil), catalog.IsRecord(point), catalog.AsRecord(nil) == nil, text(catalog.FromRecord(catalog.AsRecord(point))))
    fmt.Printf("%d %t %d\n", model.GppGetStatic_Point_identifier(), model.PointFrom(point) == point, model.PointFrom(nil).Ticks())
    escaped := model.NewPointWithIntAndDoubleAndString(-2, math.Copysign(0, -1), java.StringFromUTF16([]uint16{0xd800}))
    fmt.Println(units(escaped.ToString()))
    fmt.Println(text(model.NewPointWithIntAndDoubleAndString(0, 0, nil).ToString()))
    a := model.NewNumericWithBooleanAndByteAndShortAndCharAndIntAndLongAndFloatAndDoubleAndBoxedInteger(
        true, -31, -310, 'Ω', -123, -12345678901234, math.Float32frombits(0x7fc01234), math.Copysign(0, -1), java.NewInteger(500))
    b := model.NewNumericWithBooleanAndByteAndShortAndCharAndIntAndLongAndFloatAndDoubleAndBoxedInteger(
        true, -31, -310, 'Ω', -123, -12345678901234, math.Float32frombits(0x7fc01235), math.Copysign(0, -1), java.NewInteger(500))
    c := model.NewNumericWithBooleanAndByteAndShortAndCharAndIntAndLongAndFloatAndDoubleAndBoxedInteger(
        true, -31, -310, 'Ω', -123, -12345678901234, math.Float32frombits(0x7fc01235), 0, java.NewInteger(500))
    fmt.Printf("%t %t %t %d %s\n", a.Equals(b), a.Equals(c), a.HashCode() == b.HashCode(), a.HashCode(), text(a.ToString()))
    rate := model.NewRateWithInt(4)
    fmt.Printf("%d %d %d %s %d\n", rate.Fastest(), rate.Slowest(), rate.HashCode(), text(rate.ToString()), catalog.RejectInvalidRate())
    fmt.Printf("%d %d\n", model.NewExplicitWithInt(7).Value(), model.NewExplicitWithInt(7).HashCode())
    unit, other := model.NewUnit(), model.NewUnit()
    fmt.Printf("%t %t %d %s\n", unit.Equals(other), unit == other, unit.HashCode(), text(unit.ToString()))
    envelope := model.NewEnvelopeWithRecordAndRate(point, rate)
    fmt.Printf("%t %d %s\n", envelope.Equals(model.NewEnvelopeWithRecordAndRate(copy, model.NewRateWithInt(4))),
        envelope.HashCode(), text(envelope.ToString()))
    array := java.NewArray[int32](1, []int32{1})
    java.ArraySet(array, 0, int32(4))
    buffer := model.NewBufferWithIntArray(array)
    alias := model.NewBufferWithIntArray(buffer.Data())
    distinct := model.NewBufferWithIntArray(java.NewArray[int32](1, []int32{1}))
    fmt.Printf("%t %t %t %t\n", buffer.Equals(alias), buffer.Equals(distinct), buffer.HashCode() == alias.HashCode(),
        text(buffer.ToString()) == fmt.Sprintf("Buffer[data=[I@%x]", uint32(buffer.HashCode())))
}
`)
	if got := runObjectTestCommand(t, caller, "go", "run", "-buildvcs=false", "-mod=mod", "."); got != want {
		t.Fatalf("GPP and Java records diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}

func TestJavaAOTRecordsDiagnoseUnsupportedVarargs(t *testing.T) {
	java, release := referenceJDK(t)
	file := filepath.Join(t.TempDir(), "Arguments.java")
	writeTestFile(t, file, "package reference.records; public record Arguments(String... values) {}")
	report, err := Analyze(t.Context(), []string{file}, Options{Java: java, Release: release, MethodBodies: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = EmitGPP(report)
	if err == nil || !strings.Contains(err.Error(), "variable-arity record constructors require Java varargs lowering") {
		t.Fatalf("expected a record varargs diagnostic, got %v", err)
	}
}
