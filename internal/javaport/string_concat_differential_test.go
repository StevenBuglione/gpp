package javaport

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestJavaStringConcatenationMatchesOpenJDK(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	base := filepath.Join(root, "Base.java")
	known := filepath.Join(root, "Known.java")
	subject := filepath.Join(root, "Subject.java")
	writeTestFile(t, base, `package reference.concat.base;
public class Base { public String toString() { return "base"; } }`)
	writeTestFile(t, known, `package reference.concat;
public class Known {
    public int hashCode() { return -42; }
    public String ToString() { return "ordinary method"; }
}`)
	writeTestFile(t, subject, `package reference.concat;
import reference.concat.base.Base;
public class Subject extends Base {
    private static final String PREFIX = "prefix";
    private int trace;
    private int mode;
    private String label;
    public Subject() {}
    private Base left() { trace = trace * 10 + 1; return this; }
    private String right() { trace = trace * 10 + 3; return "right"; }
    @Override public String toString() {
        trace = trace * 10 + 2;
        label = "changed";
        if (mode == 1) return null;
        if (mode == 2) throw new IllegalArgumentException("conversion");
        return "left";
    }
    public int trace() { return trace; }
    public String ordered() { trace = 0; return left() + right(); }
    public String changedRead() { label = "old"; return left() + label; }
    public String nullString() { mode = 1; String result = left() + right(); mode = 0; return result; }
    public int abrupt() {
        trace = 0;
        mode = 2;
        try { String result = left() + right(); return 0; }
        catch (IllegalArgumentException failure) { return trace; }
        finally { mode = 0; }
    }
    public String append(Object suffix) { String text = null; return text += suffix; }
    public String call(Object value) { return value.toString(); }
    public int hash(Object value) { return value.hashCode(); }
    public String baseString() { return super.toString(); }
    public String convert(Object value) { return "" + value; }
    private boolean check() { trace = 4; return true; }
    public String nullConditional(boolean typed) {
        trace = 0;
        if (typed) return "" + (check() ? (Object) null : null);
        return "" + (check() ? null : null);
    }
    public Object plain() { return new Object(); }
    public Object array() { Base[] view = new Subject[1]; return view; }
    public String primitives(Integer box, char unit, Boolean truth, float decimal, long large) {
        return box + ":" + unit + ":" + truth + ":" + decimal + ":" + large + ":" + null;
    }
    public boolean folded(int live) {
        final String head = PREFIX;
        return (head + (13 << 2) + (true ? 'x' : 'y')) == "prefix52x" &&
            ("s" + (true ? 0 : live)) != "s0";
    }
    public boolean fresh(String value) { return (value + "") != value; }
}`)
	probe := filepath.Join(root, "Probe.java")
	writeTestFile(t, probe, `import reference.concat.Known;
import reference.concat.Subject;
class Probe {
    public static void main(String[] args) {
        var subject = new Subject();
        System.out.println(subject.ordered() + " " + subject.trace());
        System.out.println(subject.changedRead() + " " + subject.nullString() + " " + subject.abrupt());
        System.out.println(subject.append(subject) + " " + subject.call(subject) + " " + subject.baseString());
        var known = new Known();
        System.out.println(subject.convert(known) + " " + subject.hash(known) + " " + known.ToString());
        var plain = subject.plain();
        var view = subject.array();
        System.out.println(subject.convert(plain).equals("java.lang.Object@" + Integer.toHexString(subject.hash(plain))));
        System.out.println(subject.convert(view).equals("[Lreference.concat.Subject;@" + Integer.toHexString(subject.hash(view))));
        var converted = subject.primitives(null, '\ud800', true, 0.1f, -95123456789L);
        System.out.println((int) converted.charAt(5) + " " + converted.substring(6));
        System.out.println(subject.folded(9) + " " + subject.fresh("left") + " " + subject.convert(null));
        System.out.println(subject.nullConditional(true) + " " + subject.trace());
        // javac 17 drops the condition when both branches have the null type.
        if (Runtime.version().feature() >= 27 &&
            (!subject.nullConditional(false).equals("null") || subject.trace() != 4)) {
            throw new AssertionError("null branches must still evaluate their condition");
        }
    }
}`)
	inputs := []string{base, known, subject}
	classes := filepath.Join(root, "classes")
	javacArgs := append([]string{"--release", strconv.Itoa(release), "-d", classes, probe}, inputs...)
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
	var source []string
	for _, unit := range units {
		file := filepath.Join(root, unit.Name+".gpp")
		writeTestFile(t, file, unit.Source)
		source = append(source, file)
	}
	output := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions(source, output, compiler.CompileOptions{ModulePath: "example.com/java-concat"}); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(output, "main.go"), `package main
import (
    "fmt"
    "strconv"
    "unicode/utf16"

    "example.com/java-concat/gpp/java"
    "example.com/java-concat/reference/concat"
)

func text(value *java.String) string { result, _ := value.ToGo(); return result }

func main() {
    subject := concat.NewSubject()
    fmt.Println(text(subject.Ordered()), subject.Trace())
    fmt.Println(text(subject.ChangedRead()), text(subject.NullString()), subject.Abrupt())
    fmt.Println(text(subject.Append(subject.GppAsBase())), text(subject.Call(subject)), text(subject.BaseString()))
    known := concat.NewKnown()
    fmt.Println(text(subject.Convert(known)), subject.Hash(known), text(known.ToString()))
    plain := subject.Plain()
    view := subject.Array()
    fmt.Println(text(subject.Convert(plain)) == "java.lang.Object@" + strconv.FormatUint(uint64(uint32(subject.Hash(plain))), 16))
    fmt.Println(text(subject.Convert(view)) == "[Lreference.concat.Subject;@" + strconv.FormatUint(uint64(uint32(subject.Hash(view))), 16))
    converted := subject.Primitives(nil, 0xd800, java.BoxBoolean(true), 0.1, -95123456789).UTF16()
    fmt.Println(converted[5], string(utf16.Decode(converted[6:])))
    fmt.Println(subject.Folded(9), subject.Fresh(java.StringFromGo("left")), text(subject.Convert(nil)))
    fmt.Println(text(subject.NullConditional(true)), subject.Trace())
    if text(subject.NullConditional(false)) != "null" || subject.Trace() != 4 {
        panic("null branches must still evaluate their condition")
    }
}
`)
	if got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", "."); got != want {
		t.Fatalf("string concatenation diverged\nGo:\n%s\nOpenJDK:\n%s", got, want)
	}
}
