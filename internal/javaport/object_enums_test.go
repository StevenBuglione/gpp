package javaport

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestJavaEnumsMatchOpenJDK(t *testing.T) {
	java, release := referenceJDK(t)
	if release < 21 {
		t.Skip("qualified enum and null switch labels require Java 21 or newer")
	}
	root := t.TempDir()
	pkg := filepath.Join(root, "reference", "enums")
	sources := []struct {
		name, code string
	}{
		{"Events.java", `package reference.enums;
public class Events {
    private static int trace;
    public static int mark(int digit) { trace = trace * 10 + digit; return digit; }
    public static int read() { return trace; }
    public static void reset() { trace = 0; }
}`},
		{"Weighted.java", `package reference.enums;
public interface Weighted { int score(); }`},
		{"Priority.java", `package reference.enums;
@Deprecated
public enum Priority implements Weighted {
    LOW(Events.mark(1)), FAST(Events.mark(2)), CAFÉ;

    public static final int LITERAL = 900;
    private static int all = values().length;
    static { Events.mark(8); }

    private final int weight;
    private int initializedOrdinal = ordinal();
    private int initializedNameLength = name().length();
    private int recursive;
    private int constructorSwitch;
    Priority() { this(Events.mark(3)); }
    Priority(int weight) {
        this.weight = weight;
        Events.mark(4 + ordinal());
        constructorSwitch = switch(this) { case LOW -> 9; case FAST -> 8; case CAFÉ -> 7; };
        try { values(); } catch (NullPointerException expected) { recursive = 1; }
        try { valueOf(name()); } catch (IllegalArgumentException expected) { recursive = recursive * 10 + 2; }
    }

    public int score() { return weight * 100 + initializedOrdinal * 10 + initializedNameLength; }
    public int duringInit() { return recursive * 10 + constructorSwitch; }
    public static int total() { return all; }
    public static int cloned() {
        Priority[] snapshot = values();
        snapshot[0] = null;
        return values()[0].score();
    }
    public static int lookup(String name) {
        try { return valueOf(name).ordinal(); }
        catch (NullPointerException e) { return -1; }
        catch (IllegalArgumentException e) { return -2; }
    }
    public boolean same(Object other) { return this == other && equals(other); }
    public boolean hashStable() { return hashCode() == hashCode(); }
    public int order(Priority other) { return compareTo(other); }
    public int lengthAfterObjectConversion() { return ("" + (Object) this).length(); }
    private static <T> T copy(T value) { return value; }
    public String genericName() { return copy(this).name(); }

    public static int traditional(Priority value) {
        try {
            int result = 0;
            switch (value) {
                case LOW: result = 1;
                case FAST: result += 2; break;
            }
            return result;
        } catch (NullPointerException e) { return -3; }
    }
    public int exhaustive() {
        return switch (this) { case LOW -> 11; case FAST -> 22; case CAFÉ -> 33; };
    }
    public static int mixed(Object value) {
        return switch (value) {
            case Priority.FAST -> 51;
            case Weighted item -> item.score();
            case null, default -> 70;
        };
    }
}`},
		{"empty/Empty.java", `package reference.enums.empty;
public enum Empty { ; }`},
		{"Styled.java", `package reference.enums;
public enum Styled implements Weighted {
    PLAIN(Events.mark(1)),
    SPECIAL(Events.mark(2)) {
        static { Events.mark(3); }
        private int adjustment = 7;
        { adjustment += 2; }
        @Override public int score() { return super.score() * 10 + adjustment; }
        @Override public String toString() { return super.toString() + "!"; }
    };
    private final int seed;
    private final int duringInit;
    Styled(int seed) { this.seed = seed; duringInit = score(); }
    public int score() { return seed; }
    public int atInit() { return duringInit; }
    public boolean same(Object other) { return this == other; }
    public int displayLength() {
        Enum base = this;
        return ("" + (Object) this).length() * 10 + base.toString().length();
    }
    public int branch() { return switch(this) { case SPECIAL -> 101; case PLAIN -> 102; }; }
}`},
		{"AbstractChoice.java", `package reference.enums;
public enum AbstractChoice {
    FIRST { @Override public int count() { return 1; } },
    SECOND { @Override public int count() { return 2; } };

    public abstract int count();
}`},
	}
	files := make([]string, 0, len(sources))
	for _, item := range sources {
		path := filepath.Join(pkg, item.name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, path, item.code)
		files = append(files, path)
	}
	probe := filepath.Join(root, "Probe.java")
	writeTestFile(t, probe, `import reference.enums.*;
import reference.enums.empty.Empty;
class Probe {
    public static void main(String[] args) {
        System.out.println(Priority.LITERAL);
        System.out.println(Events.read());
        System.out.println(Priority.class.isAnnotationPresent(Deprecated.class) + " " + Events.read());
        var low = Priority.LOW;
        var fast = Priority.FAST;
        var café = Priority.CAFÉ;
        System.out.println(fast.genericName());
        System.out.println(Events.read());
        for (Priority value : Priority.values()) {
            System.out.println(value.name().length() + " " + value.ordinal() + " " + value.score());
            System.out.println(value.hashStable() + " " + value.exhaustive() + " " + value.lengthAfterObjectConversion() + " " + value.duringInit());
        }
        System.out.println(Priority.total());
        System.out.println(Priority.cloned());
        System.out.println(Priority.lookup("CAFÉ"));
        System.out.println(Priority.lookup("café"));
        System.out.println(Priority.lookup(" FAST"));
        System.out.println(Priority.lookup(null));
        System.out.println(low.same(Priority.valueOf("LOW")));
        System.out.println(low.same(fast));
        System.out.println(low.same(null));
        System.out.println(fast.order(low));
        System.out.println(café.order(low));
        System.out.println(Priority.traditional(low));
        System.out.println(Priority.traditional(fast));
        System.out.println(Priority.traditional(café));
        System.out.println(Priority.traditional(null));
        System.out.println(Priority.mixed(low));
        System.out.println(Priority.mixed(fast));
        System.out.println(Priority.mixed(null));
        System.out.println(Empty.values().length);
        Events.reset();
        Styled[] styled = Styled.values();
        System.out.println(Events.read());
        for (Styled value : styled) {
            System.out.println(value.name() + " " + value + " " + value.score() + " " + value.atInit());
            System.out.println(value.displayLength() + " " + value.branch());
            System.out.println(value.same(Styled.valueOf(value.name())) + " " + Priority.mixed(value));
        }
        System.out.println(AbstractChoice.FIRST.count() + " " + AbstractChoice.SECOND.count());
    }
}`)
	classes := filepath.Join(root, "classes")
	args := append([]string{"--release", strconv.Itoa(release), "-d", classes}, files...)
	args = append(args, probe)
	runObjectTestCommand(t, root, filepath.Join(filepath.Dir(java), "javac"), args...)
	want := runObjectTestCommand(t, root, java, "-cp", classes, "Probe")
	report, err := Analyze(t.Context(), files, Options{Java: java, Release: release, MethodBodies: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range report.Files {
		if file.Package != "reference.enums.empty" {
			continue
		}
		source, err := EmitGPP(Report{Files: []SourceFile{file}})
		if err != nil || !strings.Contains(source, "func EmptyValues()") {
			t.Fatalf("single enum emission did not lower Empty.values(): %v", err)
		}
	}
	units, err := EmitGPPPackages(report)
	if err != nil {
		t.Fatal(err)
	}
	inputs := make([]string, 0, len(units))
	for _, unit := range units {
		input := filepath.Join(root, unit.Name+".gpp")
		writeTestFile(t, input, unit.Source)
		inputs = append(inputs, input)
	}
	output := filepath.Join(root, "generated")
	if err := compiler.CompileFilesWithOptions(inputs, output, compiler.CompileOptions{ModulePath: "example.com/javaenums"}); err != nil {
		t.Fatalf("compile enum fixture: %v", err)
	}
	writeTestFile(t, filepath.Join(output, "main.go"), `package main
import (
    "fmt"
    "example.com/javaenums/gpp/java"
    "example.com/javaenums/reference/enums"
    "example.com/javaenums/reference/enums/empty"
)
func main() {
    fmt.Println(enums.PriorityLITERAL)
    fmt.Println(enums.EventsRead())
    fmt.Println(len(enums.GppJavaAnnotations_Priority().GetDeclaredAnnotations()) == 1, enums.EventsRead())
    low := enums.GppGetStatic_Priority_LOW()
    fast := enums.GppGetStatic_Priority_FAST()
    cafe := enums.GppGetStatic_Priority_GppEnumName_434146c389()
    genericName, _ := fast.GenericName().ToGo()
    fmt.Println(genericName)
    fmt.Println(enums.EventsRead())
    entries := enums.PriorityValues()
    for index := int32(0); index < java.ArrayLength(entries); index++ {
        value := java.ArrayGet[*enums.Priority](entries, index)
        length, err := java.StringLength(value.Name())
        if err != nil { panic(err) }
        fmt.Println(length, value.Ordinal(), value.Score())
        fmt.Println(value.HashStable(), value.Exhaustive(), value.LengthAfterObjectConversion(), value.DuringInit())
    }
    fmt.Println(enums.PriorityTotal())
    fmt.Println(enums.PriorityCloned())
    fmt.Println(enums.PriorityLookup(java.StringFromGo("CAFÉ")))
    fmt.Println(enums.PriorityLookup(java.StringFromGo("café")))
    fmt.Println(enums.PriorityLookup(java.StringFromGo(" FAST")))
    fmt.Println(enums.PriorityLookup(nil))
    fmt.Println(low.Same(enums.PriorityValueOf(java.StringFromGo("LOW"))))
    fmt.Println(low.Same(fast))
    fmt.Println(low.Same(nil))
    fmt.Println(fast.Order(low))
    fmt.Println(cafe.Order(low))
    fmt.Println(enums.PriorityTraditional(low))
    fmt.Println(enums.PriorityTraditional(fast))
    fmt.Println(enums.PriorityTraditional(cafe))
    fmt.Println(enums.PriorityTraditional(nil))
    fmt.Println(enums.PriorityMixed(low))
    fmt.Println(enums.PriorityMixed(fast))
    fmt.Println(enums.PriorityMixed((*enums.Priority)(nil)))
    fmt.Println(java.ArrayLength(empty.EmptyValues()))
    if entries == enums.PriorityValues() { panic("Java enum values reused its backing array") }
    enums.EventsReset()
    entries = enums.StyledValues()
    fmt.Println(enums.EventsRead())
    for index := int32(0); index < java.ArrayLength(entries); index++ {
        value := java.ArrayGet[*enums.Styled](entries, index)
        name, _ := value.Name().ToGo()
        display, _ := value.ToString().ToGo()
        fmt.Println(name, display, value.Score(), value.AtInit())
        fmt.Println(value.DisplayLength(), value.Branch())
        fmt.Println(value.Same(enums.StyledValueOf(value.Name())), enums.PriorityMixed(value))
    }
    fmt.Println(enums.GppGetStatic_AbstractChoice_FIRST().Count(), enums.GppGetStatic_AbstractChoice_SECOND().Count())
}
`)
	if got := runObjectTestCommand(t, output, "go", "run", "-buildvcs=false", "."); got != want {
		t.Fatalf("GPP and Java enums diverged\nGo:\n%s\nJava:\n%s", got, want)
	}
}
