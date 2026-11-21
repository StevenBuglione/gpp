package consumer

import (
	"errors"
	"slices"
	"testing"

	"example.com/gpp-interop-library/interop"
	spring "example.com/gpp-spring/org/springframework/core"
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

type orderedBean int32

func (bean orderedBean) GetOrder() int32 {
	return int32(bean)
}

func TestSpringOrdered(t *testing.T) {
	var bean spring.Ordered = orderedBean(42)
	if bean.GetOrder() != 42 ||
		spring.OrderedHIGHEST_PRECEDENCE != -2147483648 ||
		spring.OrderedLOWEST_PRECEDENCE != 2147483647 {
		t.Fatal("Spring Ordered semantics changed")
	}
}

type priorityBean struct {
	orderedBean
}

func (priorityBean) IsPriorityOrdered() {}

func TestSpringPriorityOrdered(t *testing.T) {
	if _, priority := any(orderedBean(42)).(spring.PriorityOrdered); priority {
		t.Fatal("an ordinary Ordered value became PriorityOrdered")
	}
	var prioritized spring.PriorityOrdered = priorityBean{orderedBean: 10}
	if prioritized.GetOrder() != 10 {
		t.Fatal("PriorityOrdered did not preserve its Ordered contract")
	}
}
