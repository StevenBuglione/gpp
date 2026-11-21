package consumer

import (
	"errors"
	"slices"
	"testing"

	"example.com/gpp-interop-library/interop"
)

func TestGeneratedLibrary(t *testing.T) {
	entry := interop.NewEntry(" Ada ")
	var message messageSource = entry
	if got := message.Message(); got != "baseline: Ada" {
		t.Fatalf("Message() = %q", got)
	}
	if interop.Identity(entry) != entry || interop.Identity(nil) != nil {
		t.Fatal("Identity changed a pointer")
	}
	entry.Value = "Grace"
	if message.Message() != "baseline: Grace" {
		t.Fatal("Go field update was not visible to the GPP method")
	}
	if got := interop.Collect(1, 2, 3); !slices.Equal(got, []int{1, 2, 3}) {
		t.Fatalf("Collect() = %v", got)
	}
	if value, err := interop.Lookup(" Ada "); value != "Ada" || err != nil {
		t.Fatalf("Lookup() = (%q, %v)", value, err)
	}
	if value, err := interop.Lookup(""); value != "" || !errors.Is(err, interop.ErrLookup) {
		t.Fatalf("Lookup() = (%q, %v), want the original Go error", value, err)
	}
}
