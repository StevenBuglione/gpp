package compiler

import (
	"os/exec"
	"strings"
	"testing"
)

func TestBundledGoExceptionTypes(t *testing.T) {
	if _, err := (bundledGoTypeImporter{}).Import("gpp/java"); err != nil {
		t.Fatalf("resolve bundled Java catch types: %v", err)
	}
	source, err := ParseFile("main.gpp", `package main

import (
    "fmt"
    j "gpp/java"
)

func main() {
    try {
        throw &j.IllegalArgumentException{Message: "invalid"}
    } catch j.IllegalArgumentException err {
        fmt.Println(err.Message)
    } catch j.RuntimeException err {
        fmt.Println("unexpected", err)
    }

    try {
        throw &j.IllegalArgumentException{Message: "rejected"}
    } catch j.RuntimeException err {
        fmt.Println(err.JavaClassName(), err.Error())
    }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if err := CompileProgramWithOptions(&Program{Files: []*File{source}}, directory, CompileOptions{ModulePath: "generated"}); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "run", "-buildvcs=false", ".")
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil || string(output) != "invalid\njava.lang.IllegalArgumentException rejected\n" {
		t.Fatalf("generated catch program: %v\n%s", err, output)
	}
}

func TestCatchRejectsCoveredBundledGoType(t *testing.T) {
	source, err := ParseFile("main.gpp", `package main
import "gpp/java"

func main() {
    try {
        throw &java.IllegalArgumentException{}
    } catch java.Throwable err {
    } catch java.IllegalArgumentException err {
    }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Emit(source)
	if err == nil || !strings.Contains(err.Error(), "unreachable catch: java.IllegalArgumentException is already matched by java.Throwable") {
		t.Fatalf("expected unreachable catch diagnostic, got %v", err)
	}
}
