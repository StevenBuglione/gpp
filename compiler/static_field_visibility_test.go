package compiler

import (
	"strings"
	"testing"
)

func TestResolveProgramRejectsInaccessibleStaticFields(t *testing.T) {
	const owner = `class Account {
    static pin int = 7
    static _balance int = 8
    static const passcode int = 123
}`
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{"qualified read", `func Read() int { return Account.pin }`, "private member pin"},
		{"qualified write", `func Write() { Account.pin = 2 }`, "private member pin"},
		{"unrelated class", `class Stranger { static func Read() int { return Account._balance } }`, "protected member _balance"},
		{"inherited private shorthand", `class Child : Account { static func Read() int { return pin } }`, "private member pin"},
		{"private through subclass qualifier", `class Child : Account {}
func Read() int { return Child.pin }`, "private member pin"},
		{"inherited protected", `class Child : Account {}
func Read() int { return Child._balance }`, "protected member _balance"},
		{"native method", `type Reader struct{}
func (Reader) Read() int { return Account.pin }`, "private member pin"},
		{"native package variable", `var leak = Account.passcode`, "private member passcode"},
		{"static field initializer", `class Reader { static Leaked int = Account.pin }`, "private member pin"},
		{"instance field initializer", `class Reader { Leaked int = Account.pin }`, "private member pin"},
		{"static block", `class Reader { static { _ = Account.pin } }`, "private member pin"},
		{"unshadowed after block", `func Read() int { { Account := struct{ pin int }{1}; _ = Account.pin }; return Account.pin }`, "private member pin"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			program := parseProgram(t, owner+"\n"+test.source)
			_, err := ResolveProgram(program)
			if err == nil || !strings.Contains(err.Error(), "cannot access "+test.want+" of class Account") {
				t.Fatalf("expected a visibility error for %s, got %v", test.want, err)
			}
			if _, ok := PositionOfDiagnostic(DiagnosticsOf(err)[0]); !ok {
				t.Fatalf("expected a source position for %v", err)
			}
		})
	}
}

func TestResolveProgramPrefersSubclassInstanceFieldToInheritedStatic(t *testing.T) {
	program := parseProgram(t, `class Account { static _balance int = 8 }
class Child : Account { _balance int }
func Read() int { return Child._balance }`)
	_, err := ResolveProgram(program)
	if err == nil || !strings.Contains(err.Error(), "cannot access protected member _balance of class Child") {
		t.Fatalf("the instance field on Child should hide Account's static field, got %v", err)
	}
}

func TestResolveProgramAllowsStaticFieldAccessAndLocalShadowing(t *testing.T) {
	program := parseProgram(t, `
class Account {
    static pin int = 7
    static _balance int = 8
    static Public int = pin
    static { Account.pin += 1 }
    static func Read() int { return pin + Account.pin }
}

var pin = 99

class Child : Account {
    static func Read() int { return _balance + Account._balance + Child._balance + pin }
    static func ReadLocal(Account struct{ pin int }) int { return Account.pin }
}

type Reader struct{}
func (Reader) Read(Account struct{ pin int }) int { return Account.pin }

func ReadShadow() int {
    Account := struct{ pin int }{1}
    return Account.pin
}
func ReadPublic() int { return Child.Public }
`)
	if _, err := ResolveProgram(program); err != nil {
		t.Fatalf("valid static field access or local shadowing was rejected: %v", err)
	}
}

func TestResolveProgramChecksStaticFieldVisibilityUsingFileImports(t *testing.T) {
	base := "package base\nclass Parent { static _Balance int = 1 }"
	other := "package other\nclass Parent { static _Balance int = 2 }"
	child := `package child
import alias "example.com/staticfields/base"
class Child : alias.Parent {
    static func Read() int { return _Balance + alias.Parent._Balance }
}`
	good := parseProgram(t, base, other, child, `package child
import alias "example.com/staticfields/other"
class Stranger { static func Read() int { alias := struct{ Parent struct{ _Balance int } }{}; return alias.Parent._Balance } }`)
	if _, err := ResolveProgramWithModulePath(good, "example.com/staticfields"); err != nil {
		t.Fatalf("valid inherited access or shadowing through a reused import alias was rejected: %v", err)
	}
	bad := parseProgram(t, base, other, child, `package child
import alias "example.com/staticfields/other"
func Read() int { return alias.Parent._Balance }`)
	if _, err := ResolveProgramWithModulePath(bad, "example.com/staticfields"); err == nil || !strings.Contains(err.Error(), "cannot access protected member _Balance of class Parent") {
		t.Fatalf("a file's alias must be resolved to its imported class: %v", err)
	}
}
