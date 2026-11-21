package consumer

import (
	"fmt"
	"testing"

	spring "github.com/StevenBuglione/spring-framework-gpp/org/springframework/core"
	"github.com/StevenBuglione/spring-framework-gpp/org/springframework/util/backoff"
)

func TestSpringOrdered(t *testing.T) {
	var bean spring.Ordered = orderedBean(42)
	if bean.GetOrder() != 42 ||
		spring.OrderedHIGHEST_PRECEDENCE != -2147483648 ||
		spring.OrderedLOWEST_PRECEDENCE != 2147483647 {
		t.Fatal("Spring Ordered semantics changed")
	}
}

func TestSpringPriorityOrdered(t *testing.T) {
	if _, priority := any(orderedBean(42)).(spring.PriorityOrdered); priority {
		t.Fatal("an ordinary Ordered value became PriorityOrdered")
	}
	var prioritized spring.PriorityOrdered = priorityBean{orderedBean: 10}
	if prioritized.GetOrder() != 10 {
		t.Fatal("PriorityOrdered did not preserve its Ordered contract")
	}
}

func TestSpringFixedBackOff(t *testing.T) {
	var strategy backoff.BackOff = backoff.NewFixedBackOff()
	steady := strategy.Start()
	for attempt := 0; attempt < 100; attempt++ {
		if delay := steady.NextBackOff(); delay != backoff.FixedBackOffDEFAULT_INTERVAL {
			t.Fatalf("default delay = %d", delay)
		}
	}
	if got := fmt.Sprint(strategy); got != "FixedBackOff[interval=5000, maxAttempts=unlimited]" {
		t.Fatalf("default description = %q", got)
	}
	if got := backoff.NewFixedBackOffWithInterval(25).Start().NextBackOff(); got != 25 {
		t.Fatalf("interval constructor delay = %d", got)
	}
	if got := backoff.NewFixedBackOffWithLimit(100, 0).Start().NextBackOff(); got != backoff.BackOffExecutionSTOP {
		t.Fatalf("zero attempts delay = %d", got)
	}

	limited := backoff.NewFixedBackOffWithLimit(100, 1)
	first, second := limited.Start(), limited.Start()
	if first == second {
		t.Fatal("Start reused the existing execution")
	}
	for _, execution := range []backoff.BackOffExecution{first, second} {
		if got := execution.NextBackOff(); got != 100 {
			t.Fatalf("first attempt delay = %d", got)
		}
	}
	limited.SetInterval(200)
	limited.SetMaxAttempts(2)
	for _, execution := range []backoff.BackOffExecution{first, second} {
		if got := execution.NextBackOff(); got != 200 {
			t.Fatalf("live update delay = %d", got)
		}
		if got := fmt.Sprint(execution); got != "FixedBackOffExecution[interval=200, currentAttempts=2, maxAttempts=2]" {
			t.Fatalf("execution description = %q", got)
		}
		if got := execution.NextBackOff(); got != backoff.BackOffExecutionSTOP {
			t.Fatalf("exhausted delay = %d", got)
		}
	}
}
