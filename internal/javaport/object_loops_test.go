package javaport

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestJavaStructuredLoopsMatchOpenJDK(t *testing.T) {
	java, release := referenceJDK(t)
	root := t.TempDir()
	source := filepath.Join(root, "Loops.java")
	writeTestFile(t, source, `package audit.loops;

import java.io.IOException;

public final class Loops {
    private int trace;
    private int initial;

    { for (int index = 0; index != 2; index++) { if (index == 1) break; initial++; } }
    { for (int index = 0; index != 2; index++) { initial++; continue; } }

    public Loops() {}

    public int initial() { return initial; }

    private int mark(int digit) {
        trace = trace * 10 + digit;
        return digit;
    }

    public int classic() {
        trace = 0;
        for (int left = mark(1), right = mark(2); left != 4; ++left, mark(right)) {
            if (left == 2) continue;
            mark(3);
            right++;
        }
        int left = 0;
        int right = 0;
        for (left = mark(5), right = left; left != 7; left++, right--) {
            mark(right);
        }
        return trace;
    }

    public int whileLoop(int input) {
        trace = 0;
        int next;
        while ((next = input++) != 5) {
            if (next == 2) continue;
            mark(next);
            if (next == 3) break;
        }
        return trace * 10 + input;
    }

    public int doLoop() {
        trace = 0;
        int count = 0;
        do {
            count++;
            if (count == 1) continue;
            mark(3);
            if (count == 3) break;
        } while (mark(4) != 5);
        return trace;
    }

    public int crossed(int mode) {
        trace = 0;
        Outer: for (int index = 0; index != 2; index++, mark(6)) {
            Inner: do {
                try {
                    try {
                        mark(1);
                        if (mode == 0) continue Outer;
                        if (mode == 1) break Outer;
                        if (mode == 2) throw new IllegalArgumentException();
                        if (mode == 3) return mark(7);
                        if (mode != 6) break Inner;
                    } catch (IllegalArgumentException caught) {
                        mark(4);
                        continue Outer;
                    } finally {
                        mark(2);
                        if (mode == 1) continue Outer;
                        if (mode == 2) break Outer;
                        if (mode == 3) break Inner;
                        if (mode == 4) throw new IllegalArgumentException();
                    }
                } catch (IllegalArgumentException caught) {
                    mark(5);
                    break Outer;
                } finally {
                    mark(3);
                }
                mark(8);
            } while (mark(9) != 9);
            mark(4);
        }
        return trace;
    }

    public int nestedReturn(int flag) {
        while (flag != 0) {
            try {
                return 4;
            } finally {
                if (flag == 2) return 5;
            }
        }
        return 6;
    }

    public int labeledBlock(boolean stop) {
        trace = 0;
        Done: {
            try {
                if (stop) break Done;
                mark(1);
            } finally {
                mark(2);
            }
            mark(3);
        }
        return trace;
    }

    public int multiCatch(boolean checked) {
        trace = 0;
        try {
            if (checked) throw new IOException();
            throw new IllegalArgumentException();
        } catch (IOException | IllegalArgumentException caught) {
            for (int index = 0; index != 2; index++) {
                mark(index);
                if (index == 1) break;
                continue;
            }
        }
        return trace;
    }

    public int constantLoop() {
        do {
            return 9;
        } while (true);
    }

    private int[] selected(int[] source) {
        mark(7);
        return source;
    }

    public int enhanced() {
        trace = 0;
        int[] source = { 1, 2, 3 };
        int[] alias = source;
        Outer: for (long element : selected(source)) {
            source = new int[]{9};
            if (element == 1) {
                alias[1] = 5;
                continue Outer;
            }
            if (element == 3) break;
            mark((int) element);
        }
        return trace;
    }

    public int enhancedNulls(boolean boxed) {
        trace = 0;
        try {
            if (boxed) {
                for (int element : new Integer[]{1, null, 3}) mark(element);
            } else {
                for (int element : (int[]) null) mark(element);
            }
        } catch (NullPointerException failure) {
            mark(8);
        }
        return trace;
    }

    public int resourceBranch(AutoCloseable resource, boolean stopInFinally) throws Exception {
        trace = 0;
        for (int index = 0; index != 1; index++, mark(5)) {
            try (resource) {
                mark(1);
                continue;
            } catch (IllegalArgumentException failure) {
                mark(3);
                break;
            } finally {
                mark(4);
                if (stopInFinally) break;
            }
        }
        return trace;
    }

    public int updates() {
        byte small = 127;
        char large = 65535;
        double value = 2.5;
        int input = 3;
        return small++ + small + ++large + (int)--value + --input + input--;
    }
}`)
	probe := filepath.Join(root, "Probe.java")
	writeTestFile(t, probe, `import audit.loops.Loops;

class Probe {
    public static void main(String[] args) throws Exception {
        var loops = new Loops();
        System.out.println(loops.initial());
        System.out.println(loops.classic());
        System.out.println(loops.whileLoop(1));
        System.out.println(loops.doLoop());
        for (int mode = 0; mode != 7; mode++) System.out.println(loops.crossed(mode));
        for (int mode = 0; mode != 3; mode++) System.out.println(loops.nestedReturn(mode));
        System.out.println(loops.labeledBlock(false));
        System.out.println(loops.labeledBlock(true));
        System.out.println(loops.multiCatch(false));
        System.out.println(loops.multiCatch(true));
        System.out.println(loops.constantLoop());
        System.out.println(loops.updates());
        System.out.println(loops.enhanced());
        System.out.println(loops.enhancedNulls(false));
        System.out.println(loops.enhancedNulls(true));
        int[] closes = {0};
        System.out.println(loops.resourceBranch(() -> closes[0]++, false) + " " + closes[0]);
        System.out.println(loops.resourceBranch(() -> { closes[0]++; throw new IllegalArgumentException(); }, false) + " " + closes[0]);
        System.out.println(loops.resourceBranch(() -> closes[0]++, true) + " " + closes[0]);
    }
}`)
	classes := filepath.Join(root, "classes")
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"),
		"--release", strconv.Itoa(release), "-d", classes, source, probe)
	want := runObjectTestCommand(t, root, java, "-cp", classes, "Probe")
	report, err := Analyze(t.Context(), []string{source}, Options{Java: java, Release: release, MethodBodies: true})
	if err != nil {
		t.Fatal(err)
	}
	units, err := EmitGPPPackages(report)
	if err != nil {
		t.Fatal(err)
	}
	inputs := make([]string, 0, len(units))
	for index, unit := range units {
		input := filepath.Join(root, strconv.Itoa(index)+".gpp")
		writeTestFile(t, input, unit.Source)
		inputs = append(inputs, input)
	}
	output := filepath.Join(root, "output")
	if err := compiler.CompileFilesWithOptions(inputs, output, compiler.CompileOptions{ModulePath: "example.com/java-loops"}); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(output, "main.go"), `package main

import (
    "fmt"
    "example.com/java-loops/audit/loops"
    "example.com/java-loops/gpp/java"
)

type resource struct {
    closes *int
    javaFailure bool
    goFailure any
}

func (*resource) GppAutoCloseable() {}
func (r *resource) Close() {
    (*r.closes)++
    if r.javaFailure { java.Raise(java.NewIllegalArgumentException(nil)) }
    if r.goFailure != nil { panic(r.goFailure) }
}

func checkedBranch(receiver *loops.Loops, callback *resource, stop bool) int32 {
    value, err := receiver.GppCallResourceBranch(callback, stop)
    if err != nil { panic(err) }
    return value
}

func main() {
    receiver := loops.NewLoops()
    fmt.Println(receiver.Initial())
    fmt.Println(receiver.Classic())
    fmt.Println(receiver.WhileLoop(1))
    fmt.Println(receiver.DoLoop())
    for mode := int32(0); mode != 7; mode++ { fmt.Println(receiver.Crossed(mode)) }
    for mode := int32(0); mode != 3; mode++ { fmt.Println(receiver.NestedReturn(mode)) }
    fmt.Println(receiver.LabeledBlock(false))
    fmt.Println(receiver.LabeledBlock(true))
    fmt.Println(receiver.MultiCatch(false))
    fmt.Println(receiver.MultiCatch(true))
    fmt.Println(receiver.ConstantLoop())
    fmt.Println(receiver.Updates())
    fmt.Println(receiver.Enhanced())
    fmt.Println(receiver.EnhancedNulls(false))
    fmt.Println(receiver.EnhancedNulls(true))
    closes := 0
    fmt.Println(checkedBranch(receiver, &resource{closes: &closes}, false), closes)
    fmt.Println(checkedBranch(receiver, &resource{closes: &closes, javaFailure: true}, false), closes)
    fmt.Println(checkedBranch(receiver, &resource{closes: &closes}, true), closes)
    func() {
        marker := new(int)
        defer func() {
            if recover() != marker { panic("finally branch hid a native Go panic") }
        }()
        _ = checkedBranch(receiver, &resource{closes: &closes, goFailure: marker}, true)
    }()
}
`)
	if got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", "."); got != want {
		t.Fatalf("GPP and Java loop behavior diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}
