package retry

import (
	"testing"
	"time"
)

func TestRetryBoundaryIntegrationCapDurationNonPositive(
	t *testing.T,
) {
	for _, duration := range []time.Duration{
		0,
		-time.Nanosecond,
		-time.Second,
	} {
		if got := capDuration(duration); got != 0 {
			t.Errorf(
				"capDuration(%v) = %v, want 0",
				duration,
				got,
			)
		}
	}
}
