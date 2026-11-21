package javaport

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestJavaAssertionsMatchOpenJDK(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	probe := filepath.Join(root, "Probe.java")
	writeTestFile(t, probe, `package reference.assertion.model;
import java.io.IOException;

public final class Probe {
    private int conditions;
    private int details;

    public Probe() {}

    private boolean condition(boolean result) {
        conditions++;
        return result;
    }

    private String detail() {
        details++;
        return "detail";
    }

    public int conditions() { return conditions; }
    public int details() { return details; }

    public String check(boolean result) {
        try {
            assert condition(result) : detail();
            return "passed";
        } catch (Exception unexpected) {
            return "caught as Exception";
        } catch (Error failure) {
            if (!(failure instanceof AssertionError)) return "unexpected Error";
            return failure.getMessage();
        }
    }

    public int boxedCondition(Boolean value) {
        try {
            assert value;
            return 0;
        } catch (NullPointerException failure) {
            return 1;
        } catch (AssertionError failure) {
            if (failure.getMessage() == null) return 2;
            return 3;
        }
    }

    public int failedDetail(int divisor) {
        try {
            assert false : 1 / divisor;
            return 0;
        } catch (ArithmeticException failure) {
            return 1;
        } catch (AssertionError failure) {
            return 2;
        }
    }

    public String formatDetail(int which) {
        try {
            if (which == 1) assert false : -0.0f;
            if (which == 2) assert false : (String) null;
            if (which == 3) assert false : Integer.valueOf(137);
            return "disabled";
        } catch (AssertionError failure) {
            return failure.getMessage();
        }
    }

    public int charDetail() {
        try {
            assert false : '\uD800';
            return 0;
        } catch (AssertionError failure) {
            return failure.getMessage().charAt(0);
        }
    }

    public String objectDetail(Detail detail) {
        try {
            assert false : detail;
            return "disabled";
        } catch (AssertionError failure) {
            return failure.getMessage();
        }
    }

    public boolean throwableDetail() {
        IOException cause = new IOException("cause");
        try {
            assert false : cause;
            return false;
        } catch (AssertionError failure) {
            return failure.getCause() == cause && failure.getMessage().equals("java.io.IOException: cause");
        }
    }

    public int fail() throws AssertionError {
        assert false : 42;
        return 7;
    }
}`)
	detail := filepath.Join(root, "Detail.java")
	writeTestFile(t, detail, `package reference.assertion.model;
public final class Detail {
    private int visits;
    private int seed = switch (1) {
        default -> { assert ++visits > 0; yield 2; }
    };
    public Detail() {}
    public String toString() { visits++; return "object"; }
    public int visits() { return visits; }
    public int nested() {
        try {
            return switch (seed) { default -> { assert false; yield seed; } };
        } catch (AssertionError failure) {
            return -1;
        }
    }
}`)
	utility := filepath.Join(root, "Utility.java")
	writeTestFile(t, utility, `package reference.assertion.utility;
public final class Utility {
    private Utility() {}
    public static int cold() { return 1; }
    public static int check(boolean value) { assert value : 11; return 5; }
}`)
	configured := filepath.Join(root, "Configured.java")
	writeTestFile(t, configured, `package reference.assertion.utility;
public final class Configured {
    private Configured() {}
    public static int check() { assert false : "explicit"; return 6; }
}`)
	sources := []string{probe, detail, utility, configured}
	driver := filepath.Join(root, "Driver.java")
	writeTestFile(t, driver, `import reference.assertion.model.*;
import reference.assertion.utility.*;

class Driver {
    public static void main(String[] args) {
        boolean enabled = args.length > 0;
        var probe = new Probe();
        var loader = Probe.class.getClassLoader();
        loader.setClassAssertionStatus("reference.assertion.model.Probe", !enabled);
        System.out.printf("%s %s %d %d%n", probe.check(true), probe.check(false), probe.conditions(), probe.details());
        System.out.printf("%d %d %d %d%n", probe.boxedCondition(null), probe.boxedCondition(Boolean.FALSE), probe.failedDetail(0), probe.charDetail());
        System.out.printf("%s %s %s%n", probe.formatDetail(1), probe.formatDetail(2), probe.formatDetail(3));
        var detail = new Detail();
        System.out.printf("%s %d %b%n", probe.objectDetail(detail), detail.visits(), probe.throwableDetail());
        loader.setClassAssertionStatus("reference.assertion.model.Detail", !enabled);
        System.out.println("nested " + detail.nested());
        try { System.out.println("adapter " + probe.fail()); }
        catch (AssertionError failure) { System.out.println("adapter " + failure.getClass().getName() + " " + failure.getMessage()); }
        System.out.println("cold " + Utility.cold());
        loader.setClassAssertionStatus("reference.assertion.utility.Utility", !enabled);
        try { System.out.println("utility " + Utility.check(false)); }
        catch (AssertionError failure) { System.out.println("utility " + failure.getMessage()); }
        loader.setClassAssertionStatus("reference.assertion.utility.Configured", !enabled);
        try { System.out.println("configured " + Configured.check()); }
        catch (AssertionError failure) { System.out.println("configured " + failure.getMessage()); }
    }
}`)
	classes := filepath.Join(root, "classes")
	javacArgs := append([]string{"--release", strconv.Itoa(release), "-sourcepath", classes, "-d", classes, driver}, sources...)
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"), javacArgs...)

	report, err := Analyze(t.Context(), sources, Options{Java: java, Release: release, MethodBodies: true})
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
	output := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions(inputs, output, compiler.CompileOptions{
		ModulePath: "example.com/assertions",
	}); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(output, "main.go"), `package main

import (
    "fmt"
    "os"
    javart "example.com/assertions/gpp/java"
    "example.com/assertions/reference/assertion/model"
    "example.com/assertions/reference/assertion/utility"
)

func text(value *javart.String) string {
    result, _ := value.ToGo()
    return result
}

func staticProbe(name string, call func() int32) {
    defer func() {
        if value := recover(); value != nil {
            failure, ok := javart.UnwrapRaised(value)
            assertion, typed := failure.(*javart.AssertionError)
            if !ok || !typed { panic(value) }
            fmt.Println(name, text(assertion.GetMessage()))
        }
    }()
    fmt.Println(name, call())
}

func preserveGoCallbackPanic() {
    marker := new(int)
    defer func() {
        if recover() != marker { panic("native panic changed") }
    }()
    javart.Try(func() { panic(marker) }, func(error) bool { return true }, nil)
}

func main() {
    preserveGoCallbackPanic()
    enabled := len(os.Args) > 1
    if enabled { javart.SetDefaultAssertionStatus(true) }
    probe := model.NewProbe()
    javart.SetClassAssertionStatus("reference.assertion.model.Probe", !enabled)
    fmt.Printf("%s %s %d %d\n", text(probe.Check(true)), text(probe.Check(false)), probe.Conditions(), probe.Details())
    fmt.Printf("%d %d %d %d\n", probe.BoxedCondition(nil), probe.BoxedCondition(javart.BoxBoolean(false)), probe.FailedDetail(0), probe.CharDetail())
    fmt.Printf("%s %s %s\n", text(probe.FormatDetail(1)), text(probe.FormatDetail(2)), text(probe.FormatDetail(3)))
    detail := model.NewDetail()
    fmt.Printf("%s %d %t\n", text(probe.ObjectDetail(detail)), detail.Visits(), probe.ThrowableDetail())
    javart.SetClassAssertionStatus("reference.assertion.model.Detail", !enabled)
    fmt.Println("nested", detail.Nested())
    value, failure := probe.GppCallFail()
    if failure == nil {
        fmt.Println("adapter", value)
    } else {
        if _, exception := failure.(javart.Exception); exception { panic("AssertionError is an Exception") }
        assertion := failure.(*javart.AssertionError)
        fmt.Println("adapter", assertion.JavaClassName(), text(assertion.GetMessage()))
    }
    fmt.Println("cold", utility.UtilityCold())
    javart.SetClassAssertionStatus("reference.assertion.utility.Utility", !enabled)
    staticProbe("utility", func() int32 { return utility.UtilityCheck(false) })
    javart.SetClassAssertionStatus("reference.assertion.utility.Configured", !enabled)
    staticProbe("configured", utility.ConfiguredCheck)
}
`)
	binary := filepath.Join(output, "probe")
	runObjectTestCommand(t, output, "go", "build", "-buildvcs=false", "-o", binary, ".")
	for _, enabled := range []bool{false, true} {
		t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
			javaArgs := []string{"-cp", classes, "Driver"}
			var goArgs []string
			if enabled {
				javaArgs = append([]string{"-ea"}, javaArgs...)
				javaArgs = append(javaArgs, "enabled")
				goArgs = append(goArgs, "enabled")
			}
			want := runObjectTestCommand(t, root, java, javaArgs...)
			got := runObjectTestCommand(t, output, binary, goArgs...)
			if got != want {
				t.Fatalf("GPP and Java assertions diverged\nGo:\n%s\nJava:\n%s", got, want)
			}
		})
	}
}

func TestJavaAssertionDoesNotEstablishDefiniteAssignment(t *testing.T) {
	java, release := referenceJDK(t)
	source := filepath.Join(t.TempDir(), "Probe.java")
	writeTestFile(t, source, `package reference.assertion;
public final class Probe {
    public Probe() {}
    public int read() {
        int value;
        assert (value = 1) > 0;
        return value;
    }
}`)
	report, err := Analyze(t.Context(), []string{source}, Options{Java: java, Release: release, MethodBodies: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = EmitGPP(report)
	if err == nil || !strings.Contains(err.Error(), "variable value might not have been initialized") {
		t.Fatalf("expected javac's definite assignment diagnostic, got %v", err)
	}
}
