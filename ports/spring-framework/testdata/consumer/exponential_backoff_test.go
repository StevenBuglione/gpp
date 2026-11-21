package consumer

import (
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/StevenBuglione/spring-framework-gpp/gpp/java"
	"github.com/StevenBuglione/spring-framework-gpp/org/springframework/util/backoff"
)

var _ backoff.BackOff = (*backoff.ExponentialBackOff)(nil)

func TestSpringExponentialBackOff(t *testing.T) {
	defaults := backoff.NewExponentialBackOff()
	checkDelays(t, defaults.Start(), 2000, 3000, 4500)

	limited, err := backoff.NewExponentialBackOffWithSettings(1000, 2)
	if err != nil {
		t.Fatal(err)
	}
	limited.SetMaxInterval(10000)
	limited.SetMaxAttempts(6)
	checkDelays(t, limited.Start(), 1000, 2000, 4000, 8000, 10000, 10000, backoff.BackOffExecutionSTOP)

	limited.SetMaxElapsedTime(2000)
	first, second := limited.Start(), limited.Start()
	if first == second {
		t.Fatal("Start returned the same execution twice")
	}
	checkDelays(t, first, 1000, 2000, backoff.BackOffExecutionSTOP)
	checkDelays(t, second, 1000, 2000, backoff.BackOffExecutionSTOP)
}

func TestSpringExponentialBackOffJitter(t *testing.T) {
	strategy := backoff.NewExponentialBackOff()
	if err := strategy.SetJitter(100); err != nil {
		t.Fatal(err)
	}
	for range 30 {
		execution := strategy.Start()
		for _, limits := range [][2]int64{{2000, 2100}, {2850, 3150}, {4275, 4725}} {
			if delay := execution.NextBackOff(); delay < limits[0] || delay >= limits[1] {
				t.Fatalf("delay = %d, want [%d, %d)", delay, limits[0], limits[1])
			}
		}
	}

	strategy.SetInitialInterval(0)
	strategy.SetMaxInterval(50)
	if delay := strategy.Start().NextBackOff(); delay < 0 || delay >= 50 {
		t.Fatalf("zero-interval delay = %d, want [0, 50)", delay)
	}
}

func TestSpringExponentialBackOffValidationAndFormatting(t *testing.T) {
	strategy := backoff.NewExponentialBackOff()
	err := strategy.SetMultiplier(math.NaN())
	var illegal *java.IllegalArgumentException
	if !errors.As(err, &illegal) || err.Error() !=
		"Invalid multiplier 'NaN': Should be greater than or equal to 1. A multiplier of 1 is equivalent to a fixed interval." {
		t.Fatalf("unexpected multiplier validation result: %v", err)
	}
	var unchecked java.RuntimeException
	if !errors.As(err, &unchecked) || unchecked.JavaClassName() != "java.lang.IllegalArgumentException" {
		t.Fatalf("lost unchecked Java exception identity: %v", err)
	}
	if strategy.GetMultiplier() != 1.5 {
		t.Fatal("invalid multiplier changed the strategy")
	}
	err = strategy.SetJitter(-1)
	if !errors.As(err, &illegal) || err.Error() != "Invalid jitter '-1': must be >= 0." {
		t.Fatalf("unexpected jitter validation result: %v", err)
	}
	if strategy.GetJitter() != 0 {
		t.Fatal("invalid jitter changed the strategy")
	}
	if result, err := backoff.NewExponentialBackOffWithSettings(100, 0.9); result != nil || !errors.As(err, &illegal) {
		t.Fatalf("invalid constructor result = (%v, %v)", result, err)
	}

	configured, err := backoff.NewExponentialBackOffWithSettings(2000, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := "ExponentialBackOff[initialInterval=2000, jitter=0, multiplier=2.0, " +
		"maxInterval=30000, maxElapsedTime=9223372036854775807, maxAttempts=9223372036854775807]"
	if got := fmt.Sprint(configured); got != want {
		t.Fatalf("description = %q, want %q", got, want)
	}
	execution := configured.Start()
	if got := fmt.Sprint(execution); got != "ExponentialBackOffExecution[currentInterval=n/a, multiplier=2.0, attempts=0]" {
		t.Fatalf("fresh execution description = %q", got)
	}
	checkDelays(t, execution, 2000)
	if got := fmt.Sprint(execution); got != "ExponentialBackOffExecution[currentInterval=2000ms, multiplier=2.0, attempts=1]" {
		t.Fatalf("execution description = %q", got)
	}
}

func TestSpringExponentialBackOffSaturatesJavaLong(t *testing.T) {
	strategy, err := backoff.NewExponentialBackOffWithSettings(2, math.Inf(1))
	if err != nil {
		t.Fatal(err)
	}
	strategy.SetMaxInterval(math.MaxInt64)
	strategy.SetMaxAttempts(2)
	checkDelays(t, strategy.Start(), 2, math.MaxInt64, backoff.BackOffExecutionSTOP)
}

func checkDelays(t *testing.T, execution backoff.BackOffExecution, expected ...int64) {
	t.Helper()
	for index, want := range expected {
		if got := execution.NextBackOff(); got != want {
			t.Fatalf("attempt %d delay = %d, want %d", index+1, got, want)
		}
	}
}
