package rest

import (
	"testing"
	"time"
)

func TestRateLimitRetryAfterMatchesUTCMinuteReset(t *testing.T) {
	for _, test := range []struct {
		second int
		want   string
	}{{0, "60"}, {1, "59"}, {30, "30"}, {59, "1"}} {
		now := time.Date(2026, 10, 6, 12, 0, test.second, 900000000, time.UTC)
		if got := rateLimitRetryAfter(now); got != test.want {
			t.Fatalf("second=%d got %q want %q", test.second, got, test.want)
		}
	}
}
