package postgrest

import (
	"testing"
	"time"
)

func TestComputeBackoffDelayCap(t *testing.T) {
	if got := computeBackoffDelay(time.Second, 0); got != time.Second {
		t.Fatalf("attempt 0 = %v, want 1s", got)
	}
	if got := computeBackoffDelay(time.Second, 3); got != 8*time.Second {
		t.Fatalf("attempt 3 = %v, want 8s", got)
	}
	if got := computeBackoffDelay(time.Second, 10); got != 30*time.Second {
		t.Fatalf("attempt 10 = %v, want 30s cap", got)
	}
	if got := computeBackoffDelay(45*time.Second, 0); got != 30*time.Second {
		t.Fatalf("base > 30s = %v, want 30s cap", got)
	}
}
