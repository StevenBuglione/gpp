package javaport

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestJavaArraysMatchOpenJDK(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	base := filepath.Join(root, "Base.java")
	writeTestFile(t, base, `package reference.arrays.base;
public class Base { public int value() { return 10; } }
`)
	child := filepath.Join(root, "Child.java")
	writeTestFile(t, child, `package reference.arrays.child;
import reference.arrays.base.Base;
public final class Child extends Base { @Override public int value() { return 20; } }
`)
	cases := filepath.Join(root, "ArrayCases.java")
	writeTestFile(t, cases, `package reference.arrays.probe;
import reference.arrays.base.Base;
import reference.arrays.child.Child;

public class ArrayCases {
    private int[] selected;
    private int sequence;
    private int[] members = {3, 5};
    public int[] empty() { return new int[] {}; }
    public int fromField() { return members[1]; }
    public int read(int[] value) { return value[0]; }
    public int write(int[] value) { return (value[0]) = 12; }
    public int[] initialize(int first) {
        int[] values = {first, 2};
        values[1] = values[1] + 3;
        return values;
    }
    public byte[] bytes() { return new byte[] {-128, 127}; }
    public double primitiveDefault() { return (new double[1])[0]; }
    public boolean referenceDefault() { return (new Child[1])[0] == null; }
    public int[][] matrix(int rows, int columns) { return new int[rows][columns]; }
    public boolean partial() { return (new int[2][])[0] == null; }
    public int jagged() {
        int[][] values = {{4}, {}, {7, 8}};
        return values[1].length + values[2][1];
    }
    public Base[] covariant() { return new Child[] {new Child()}; }
    public Base baseLoad(Base[] values) { return values[0]; }
    public int store(Base[] values, Base element) {
        try { values[0] = element; return 10; }
        catch (ArrayStoreException failure) { return 11; }
    }
    public int wrongRow() {
        Object[] outer = new int[1][1];
        try { outer[0] = new Integer[1]; return 20; }
        catch (ArrayStoreException failure) { return 21; }
    }
    public boolean arrayIdentity(int[] first, int[] second) { return first == second; }
    public boolean objectIdentity(int[] first, Object second) { return first == second; }
    public boolean primitiveInstance(Object value) { return value instanceof int[]; }
    public boolean classInstance(Object value) { return value instanceof Base[]; }
    public boolean referenceOuter(Object value) { return value instanceof Object[]; }
    public Child[] checkedCast(Object value) { return (Child[]) value; }
    public int failedCast(Object value) {
        try { return ((int[]) value).length; }
        catch (ClassCastException failure) { return 30; }
    }
    public int length(int[] values) {
        try { return values.length; }
        catch (NullPointerException failure) { return 40; }
    }
    private int[] select() { sequence = sequence * 10 + 1; return selected; }
    private int index() { sequence = sequence * 10 + 2; return 2; }
    private int element() { sequence = sequence * 10 + 3; return 7; }
    private int negative() { sequence = sequence * 10 + 4; return -1; }
    public int failedLoad(int[] values) {
        sequence = 0;
        selected = values;
        try { return select()[index()]; }
        catch (NullPointerException failure) { return sequence * 10 + 5; }
        catch (IndexOutOfBoundsException failure) { return sequence * 10 + 6; }
    }
    public int failedStore(int[] values) {
        sequence = 0;
        selected = values;
        try { select()[index()] = element(); return 0; }
        catch (NullPointerException failure) { return sequence * 10 + 5; }
        catch (ArrayIndexOutOfBoundsException failure) { return sequence * 10 + 6; }
    }
    public int negativeDimensions() {
        sequence = 0;
        try { return (new int[negative()][element()]).length; }
        catch (NegativeArraySizeException failure) { return sequence; }
    }
    public int negativeInnerDespiteZeroOuter() {
        sequence = 0;
        try { return (new int[0][negative()]).length; }
        catch (NegativeArraySizeException failure) { return sequence; }
    }
    public boolean emptySuppressionIdentity() {
        var failure = new IllegalArgumentException();
        return failure.getSuppressed() == failure.getSuppressed();
    }
}
`)
	probe := filepath.Join(root, "Probe.java")
	writeTestFile(t, probe, `import reference.arrays.base.Base;
import reference.arrays.child.Child;
import reference.arrays.probe.ArrayCases;
class Probe {
    public static void main(String[] args) {
        var cases = new ArrayCases();
        var a = cases.initialize(9);
        var foreign = new int[1];
        foreign[0] = 18;
        System.out.printf("%d %d %d %d %d %d %d %b%n", cases.fromField(), cases.read(a), a[1],
            cases.read(foreign), cases.write(foreign), cases.read(foreign), cases.bytes()[0],
            Double.doubleToRawLongBits(cases.primitiveDefault()) == 0);
        var matrix = cases.matrix(2, 3);
        System.out.printf("%b %b %d %d %d %d %b%n", cases.referenceDefault(), cases.partial(), cases.jagged(),
            matrix.length, matrix[1].length, matrix[1][2], matrix[0] != matrix[1]);
        Base[] covariant = cases.covariant();
        var child = new Child();
        System.out.printf("%d %d %d %b %d %b %d%n", cases.baseLoad(covariant).value(),
            cases.store(covariant, new Base()), cases.store(covariant, child),
            cases.baseLoad(covariant) == child, cases.store(covariant, null),
            cases.baseLoad(covariant) == null, cases.wrongRow());
        var empty = cases.empty();
        System.out.printf("%b %b %b %b %b %b %b %b %b %b %d%n",
            cases.arrayIdentity(empty, empty), cases.arrayIdentity(empty, cases.empty()),
            cases.objectIdentity(a, a), cases.primitiveInstance(a), cases.primitiveInstance(null),
            cases.classInstance(covariant), cases.referenceOuter(matrix), cases.referenceOuter(a),
            cases.checkedCast(covariant) == covariant, cases.checkedCast(null) == null,
            cases.failedCast(covariant));
        System.out.printf("%d %d %d %d %d %d %d %d %b%n", cases.length(a), cases.length(null),
            cases.failedLoad(null), cases.failedLoad(empty), cases.failedStore(null),
            cases.failedStore(empty), cases.negativeDimensions(), cases.negativeInnerDespiteZeroOuter(),
            cases.emptySuppressionIdentity());
    }
}
`)
	inputs := []string{base, child, cases}
	classes := filepath.Join(root, "classes")
	args := append([]string{"--release", strconv.Itoa(release), "-d", classes, probe}, inputs...)
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"), args...)
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
	output := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions(sources, output, compiler.CompileOptions{
		ModulePath: "example.com/java-arrays",
	}); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(root, "external")
	if err := os.Mkdir(external, 0755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(external, "go.mod"), `module example.com/array-consumer

go 1.27

require example.com/java-arrays v0.0.0

replace example.com/java-arrays => ../generated
`)
	writeTestFile(t, filepath.Join(external, "main.go"), `package main

import (
    "fmt"
    "math"

    java "example.com/java-arrays/gpp/java"
    "example.com/java-arrays/reference/arrays/base"
    "example.com/java-arrays/reference/arrays/child"
    "example.com/java-arrays/reference/arrays/probe"
)

func main() {
    cases := probe.NewArrayCases()
    a := cases.Initialize(9)
    foreign := java.NewArray[int32](1, []int32{1})
    java.ArraySet[int32](foreign, 0, 18)
    fmt.Println(cases.FromField(), cases.Read(a), java.ArrayGet[int32](a, 1), cases.Read(foreign),
        cases.Write(foreign), cases.Read(foreign), java.ArrayGet[int8](cases.Bytes(), 0),
        math.Float64bits(cases.PrimitiveDefault()) == 0)
    matrix := cases.Matrix(2, 3)
    row0, row1 := java.ArrayGet[*java.Array](matrix, 0), java.ArrayGet[*java.Array](matrix, 1)
    fmt.Println(cases.ReferenceDefault(), cases.Partial(), cases.Jagged(),
        java.ArrayLength(matrix), java.ArrayLength(row1), java.ArrayGet[int32](row1, 2), row0 != row1)
    covariant := cases.Covariant()
    selected := child.NewChild()
    fmt.Println(cases.BaseLoad(covariant).Value(), cases.Store(covariant, base.NewBase()),
        cases.Store(covariant, selected.GppAsBase()), cases.BaseLoad(covariant) == selected.GppAsBase(),
        cases.Store(covariant, nil), cases.BaseLoad(covariant) == nil, cases.WrongRow())
    empty := cases.Empty()
    fmt.Println(cases.ArrayIdentity(empty, empty), cases.ArrayIdentity(empty, cases.Empty()),
        cases.ObjectIdentity(a, a), cases.PrimitiveInstance(a), cases.PrimitiveInstance((*java.Array)(nil)),
        cases.ClassInstance(covariant), cases.ReferenceOuter(matrix), cases.ReferenceOuter(a),
        cases.CheckedCast(covariant) == covariant, cases.CheckedCast(nil) == nil, cases.FailedCast(covariant))
    fmt.Println(cases.Length(a), cases.Length(nil), cases.FailedLoad(nil), cases.FailedLoad(empty),
        cases.FailedStore(nil), cases.FailedStore(empty), cases.NegativeDimensions(),
        cases.NegativeInnerDespiteZeroOuter(), cases.EmptySuppressionIdentity())
}
`)
	got := runObjectTestCommand(t, external, "go", "run", "-buildvcs=false", ".")
	if want != got {
		t.Fatalf("Go and OpenJDK arrays diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}
