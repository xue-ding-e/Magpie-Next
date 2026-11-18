package provider

import (
	"math"
	"testing"
	"time"
)

func TestResetPriorityDue(t *testing.T) {
	now := time.Unix(1800000000, 0)
	soon := now.Add(2 * time.Hour)
	var r ResetPriority
	r.Hours = 2
	var limit Limit
	limit.Used, limit.Resets, limit.Span = 30, soon, 7*24*time.Hour
	for _, tc := range []struct {
		name   string
		window string
		span   time.Duration
		want   bool
	}{
		{"default weekly", "", 7 * 24 * time.Hour, true},
		{"weekly", "week", 7 * 24 * time.Hour, true},
		{"short is not weekly", "week", 5 * time.Hour, false},
		{"month is not weekly", "week", 30 * 24 * time.Hour, false},
		{"unknown is not weekly", "week", 0, false},
		{"monthly", "month", 30 * 24 * time.Hour, true},
		{"February", "month", 28 * 24 * time.Hour, true},
		{"long month", "month", 31 * 24 * time.Hour, true},
		{"week is not monthly", "month", 7 * 24 * time.Hour, false},
		{"daily", "day", 24 * time.Hour, true},
		{"short", "short", 5 * time.Hour, true},
		{"day is not short", "short", 24 * time.Hour, false},
		{"unknown is not short", "short", 0, false},
		{"any known reset", "any", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r.Window, limit.Span = tc.window, tc.span
			got := r.Due(Allowance{limit}, "model", now)
			if got.IsZero() == tc.want || !got.IsZero() && !got.Equal(soon) {
				t.Fatalf("reset %v, want qualifying %v at %v", got, tc.want, soon)
			}
		})
	}
	r.Window, limit.Span = "week", 7*24*time.Hour
	for _, at := range []time.Time{time.Time{}, now, now.Add(-time.Second), soon.Add(time.Nanosecond)} {
		limit.Resets = at
		if got := r.Due(Allowance{limit}, "m", now); !got.IsZero() {
			t.Errorf("unknown, passed or outside reset %v was preferred", at)
		}
	}
	limit.Resets, limit.Model = soon, "opus"
	if got := r.Due(Allowance{limit}, "sonnet", now); !got.IsZero() {
		t.Fatal("another model's window was preferred")
	}
	if got := r.Due(Allowance{limit}, "OPUS", now); !got.Equal(soon) {
		t.Fatal("matching model did not qualify")
	}
	limit.Used = 100
	if got := r.Due(Allowance{limit}, "opus", now); !got.IsZero() {
		t.Fatal("spent quota was preferred")
	}
	limit.Used, limit.Model, limit.Resets = 30, "", now.Add(time.Hour)
	other := limit
	other.Resets = soon
	if got := r.Due(Allowance{other, limit}, "m", now); !got.Equal(limit.Resets) {
		t.Fatal("earliest matching window did not decide")
	}
	for _, hours := range []float64{0, -1, 8761, math.NaN(), math.Inf(1)} {
		r.Hours = hours
		if got := r.Due(Allowance{limit}, "m", now); !got.IsZero() {
			t.Errorf("disabled or invalid hours %v preferred a window", hours)
		}
	}
}

func TestResetPriorityParse(t *testing.T) {
	for _, value := range []string{"", "off", "0h"} {
		if r, err := ParseResetPriority(value); err != nil || r != nil {
			t.Fatalf("%s: %+v %v", value, r, err)
		}
	}
	for _, value := range []string{"24h", "24h,week,routing", "30m,short,soonest"} {
		r, err := ParseResetPriority(value)
		if err != nil || r == nil {
			t.Fatalf("%s: %+v %v", value, r, err)
		}
		if value == "24h" && (r.Hours != 24 || r.Window != "week" || r.Order != "") {
			t.Fatalf("defaults: %+v", r)
		}
		if value == "30m,short,soonest" && (r.Hours != 0.5 || r.Window != "short" || r.Order != "soonest") {
			t.Fatalf("fractional hours: %+v", r)
		}
	}
	for _, value := range []string{"-1h", "8761h", "NaN", "24h,year", "24h,week,bad", "24h,week,soonest,extra"} {
		if _, err := ParseResetPriority(value); err == nil {
			t.Errorf("accepted invalid setting %q", value)
		}
	}
}
