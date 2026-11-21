package javaport

import (
	"bytes"
	"fmt"
	"math"
	"math/rand/v2"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/telgatech/gpp/compiler"
)

func TestJavaPrimitiveRuntimeAgainstJDK(t *testing.T) {
	java, release := referenceJDK(t)
	if release != 27 {
		t.Skip("the decimal-formatting contract uses Java SE 27")
	}

	output := t.TempDir()
	err := compiler.CompileFilesWithOptions(
		[]string{filepath.Join("testdata", "primitive_probe.gpp")}, output,
		compiler.CompileOptions{ModulePath: "example.com/java-primitive-probe"},
	)
	if err != nil {
		t.Fatal(err)
	}

	input := primitiveProbeInput()
	javaCommand := exec.CommandContext(t.Context(), java, filepath.Join("testdata", "JavaPrimitiveProbe.java"))
	javaCommand.Stdin = bytes.NewReader(input)
	reference, err := javaCommand.Output()
	if err != nil {
		if failure, ok := err.(*exec.ExitError); ok {
			t.Fatalf("Java reference: %v\n%s", err, failure.Stderr)
		}
		t.Fatal(err)
	}

	goCommand := exec.CommandContext(t.Context(), "go", "run", "-buildvcs=false", ".")
	goCommand.Dir = output
	goCommand.Stdin = bytes.NewReader(input)
	native, err := goCommand.Output()
	if err != nil {
		if failure, ok := err.(*exec.ExitError); ok {
			t.Fatalf("native Go probe: %v\n%s", err, failure.Stderr)
		}
		t.Fatal(err)
	}
	referenceLines := bytes.Split(reference, []byte("\n"))
	nativeLines := bytes.Split(native, []byte("\n"))
	if len(nativeLines) != len(referenceLines) {
		t.Fatalf("native probe returned %d lines, Java returned %d", len(nativeLines), len(referenceLines))
	}
	for i, line := range nativeLines {
		if !bytes.Equal(line, referenceLines[i]) {
			t.Fatalf("primitive mismatch for probe %d\nGo:   %s\nJava: %s", i+1, line, referenceLines[i])
		}
	}
}

func primitiveProbeInput() []byte {
	var input bytes.Buffer
	samples := []float64{
		0, math.Copysign(0, -1), math.Inf(1), math.Inf(-1), math.NaN(),
		math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64,
		0.8, -0.8, 1.9, -1.9, 0.001, 0.0001, 9999999, 10000000,
		math.Nextafter(0x1p31, 0), 0x1p31, -0x1p31, math.Nextafter(-0x1p31, math.Inf(-1)),
		math.Nextafter(0x1p63, 0), 0x1p63, -0x1p63, math.Nextafter(-0x1p63, math.Inf(-1)),
	}
	for _, sample := range samples {
		fmt.Fprintf(&input, "%016x\n", math.Float64bits(sample))
	}
	for exponent := range uint64(2047) {
		for _, fraction := range []uint64{0, 1, 2, (1 << 51) - 1, (1 << 52) - 1} {
			fmt.Fprintf(&input, "%016x\n", exponent<<52|fraction)
		}
	}
	for exponent := range uint64(255) {
		for _, fraction := range []uint64{0, 1, 2, (1 << 22) - 1, (1 << 23) - 1} {
			fmt.Fprintf(&input, "%016x\n", exponent<<23|fraction)
		}
	}
	for exponent := -323; exponent <= 308; exponent++ {
		power := math.Pow10(exponent)
		for _, value := range []float64{math.Nextafter(power, 0), power, math.Nextafter(power, math.Inf(1))} {
			fmt.Fprintf(&input, "%016x\n", math.Float64bits(value))
		}
	}
	random := rand.New(rand.NewPCG(27, 51))
	for range 16000 {
		fmt.Fprintf(&input, "%016x\n", random.Uint64())
	}
	return input.Bytes()
}
