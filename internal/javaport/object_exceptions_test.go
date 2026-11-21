package javaport

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestJavaTryCatchFinallyMatchesReference(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	source := filepath.Join(root, "Protection.java")
	writeTestFile(t, source, `package reference.protection;
import java.io.IOException;

public class Protection {
    private int state;
    private int cleanup;

    public Protection() {
        try { state = 1; return; }
        finally { cleanup = 2; }
    }
    public Protection(boolean checked) throws IOException {
        this();
        if (checked) { throw new IOException("constructor"); }
    }
    public int getState() { return state; }
    public int getCleanup() { return cleanup; }

    public int codeUnit(String value, int index) {
        try { state = 10; return value.charAt(index); }
        catch (StringIndexOutOfBoundsException failure) { state = 20; return 100; }
        catch (RuntimeException failure) { state = 21; return 200; }
        finally { cleanup = 30; }
    }

    public String rethrow(String message) {
        try {
            try { return fail(message); }
            catch (IOException | IllegalArgumentException failure) { throw (failure); }
            finally { cleanup = 70; }
        }
        catch (IOException failure) { return failure.getMessage(); }
    }

    public String fail(String message) throws IOException { throw new IOException(message); }

    public int overrideReturn(boolean override) {
        try { state = 7; return state; }
        finally { state = 8; if (override) { return 9; } }
    }

    public int overrideThrow(boolean override) {
        try { throw new IllegalArgumentException("original"); }
        finally { if (override) { return 17; } }
    }

    public int finalThrow() {
        try {
            try { return 11; }
            finally { throw new IllegalArgumentException("replacement"); }
        }
        catch (IllegalArgumentException failure) { return 27; }
    }

    public int throwNull() {
        try { throw null; }
        catch (NullPointerException failure) { return 44; }
    }

    public int broad() {
        try { throw new IOException(); }
        catch (Exception failure) { return 75; }
    }
}`)
	probe := filepath.Join(root, "Probe.java")
	writeTestFile(t, probe, `package reference.protection;
import java.io.IOException;

class Probe {
    public static void main(String[] args) {
        var example = new Protection();
        System.out.println(example.getState() + " " + example.getCleanup());
        for (String value : new String[]{"Q", "", null}) {
            System.out.println(example.codeUnit(value, 0) + " " + example.getState() + " " + example.getCleanup());
        }
        var message = new String(new char[]{0x0069, 0xd800});
        System.out.println((example.rethrow(message) == message) + " " + example.getCleanup());
        try { example.fail(message); }
        catch (IOException failure) { System.out.println(failure.getClass().getName() + " " + (failure.getMessage() == message)); }
        Protection constructed = null;
        try { constructed = new Protection(true); }
        catch (IOException failure) { System.out.println((constructed == null) + " " + failure.getMessage()); }
        System.out.println(example.overrideReturn(false) + " " + example.getState() + " " + example.overrideReturn(true));
        System.out.println(example.overrideThrow(true) + " " + example.finalThrow() + " " + example.throwNull() + " " + example.broad());
        try { example.overrideThrow(false); }
        catch (RuntimeException failure) { System.out.println(failure.getClass().getName()); }
    }
}`)
	classes := filepath.Join(root, "classes")
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"),
		"--release", strconv.Itoa(release), "-d", classes, source, probe)
	want := runObjectTestCommand(t, root, java, "-cp", classes, "reference.protection.Probe")
	report, err := Analyze(t.Context(), []string{source}, Options{Java: java, Release: release, MethodBodies: true})
	if err != nil {
		t.Fatal(err)
	}
	translated, err := EmitGPP(report)
	if err != nil {
		t.Fatal(err)
	}
	gpp := filepath.Join(root, "protection.gpp")
	writeTestFile(t, gpp, translated)
	output := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions([]string{gpp}, output,
		compiler.CompileOptions{ModulePath: "example.com/java-protection"}); err != nil {
		t.Fatalf("compile translated exception handling: %v\n%s", err, translated)
	}
	writeTestFile(t, filepath.Join(output, "main.go"), `package main
import (
    "errors"
    "fmt"
    "example.com/java-protection/gpp/java"
    "example.com/java-protection/reference/protection"
)
func main() {
    example := protection.NewProtection()
    fmt.Println(example.GetState(), example.GetCleanup())
    for _, value := range []*java.String{java.StringFromGo("Q"), java.StringFromGo(""), nil} {
        fmt.Println(example.CodeUnit(value, 0), example.GetState(), example.GetCleanup())
    }
    message := java.StringFromUTF16([]uint16{0x0069, 0xd800})
    fmt.Println(example.Rethrow(message) == message, example.GetCleanup())
    returned, err := example.GppCallFail(message)
    var checked *java.IOException
    if returned != nil || !errors.As(err, &checked) { panic("missing checked error from Go adapter") }
    fmt.Println(checked.JavaClassName(), checked.GetMessage() == message)
    constructed, err := protection.GppNewProtectionWithBoolean(true)
    if !errors.As(err, &checked) { panic("missing checked constructor error from Go adapter") }
    fmt.Println(constructed == nil, checked.Error())
    fmt.Println(example.OverrideReturn(false), example.GetState(), example.OverrideReturn(true))
    fmt.Println(example.OverrideThrow(true), example.FinalThrow(), example.ThrowNull(), example.Broad())
    func() {
        defer func() {
            failure, ok := java.UnwrapRaised(recover())
            if !ok { panic("expected a translated Java exception") }
            fmt.Println(failure.(java.Throwable).JavaClassName())
        }()
        _ = example.OverrideThrow(false)
    }()
    nativePanic := errors.New("native panic")
    func() {
        defer func() {
            if recovered := recover(); recovered != nativePanic { panic("Java finally hid a native Go panic") }
        }()
        java.Try(func() { panic(nativePanic) }, nil, func() { java.Return(int32(100)) })
    }()
}
`)
	got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", ".")
	if got != want || !strings.HasPrefix(want, "1 2\n81 10 30\n100 20 30\n200 21 30\n") {
		t.Fatalf("Go and JDK exception handling diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}

func TestJavaEmissionRejectsUnmappedJavaResourceType(t *testing.T) {
	java, release := referenceJDK(t)
	file := filepath.Join(t.TempDir(), "Resources.java")
	writeTestFile(t, file, `package reference.protection;
public class Resources {
    public void close() {
        try (java.io.Reader reader = new java.io.StringReader("")) {}
        catch (java.io.IOException failure) {}
    }
}`)
	report, err := Analyze(t.Context(), []string{file}, Options{Java: java, Release: release, MethodBodies: true})
	if err != nil || len(report.Diagnostics) != 0 {
		t.Fatalf("expected successful javac attribution: %v %+v", err, report.Diagnostics)
	}
	if text, err := EmitGPP(report); text != "" || err == nil || !strings.Contains(err.Error(), "unsupported Java resource type java.io.Reader") {
		t.Fatalf("expected a diagnostic before emission, got %q with %v", text, err)
	}
}

func TestJavaEmissionRejectsUnhandledCheckedException(t *testing.T) {
	java, release := referenceJDK(t)
	file := filepath.Join(t.TempDir(), "Unchecked.java")
	writeTestFile(t, file, `package reference.protection;
public class Unchecked {
    public void fail() { throw new java.io.IOException(); }
}`)
	report, err := Analyze(t.Context(), []string{file}, Options{Java: java, Release: release, MethodBodies: true})
	if err != nil {
		t.Fatal(err)
	}
	if text, err := EmitGPP(report); text != "" || err == nil || !strings.Contains(err.Error(), "java.io.IOException") {
		t.Fatalf("expected javac to reject an undeclared checked exception, got %q with %v", text, err)
	}
}
