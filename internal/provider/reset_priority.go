package provider

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// ResetPriority spends quota that will reset within Hours before the rest of
// a provider's pool. Window selects week (the default), month, day, short
// (under a day), or any. Order is empty for the provider's Routing, or
// "soonest" for the earliest reset first. Zero hours turns it off.
type ResetPriority struct {
	Hours  float64 `json:"hours"`
	Window string  `json:"window,omitempty"`
	Order  string  `json:"order,omitempty"`
}

// ParseResetPriority reads the CLI's hours[,window[,order]], for example
// "24h,week,soonest". A duration alone uses weekly quota and current routing.
func ParseResetPriority(value string) (*ResetPriority, error) {
	if value == "off" || value == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	if len(parts) > 3 {
		return nil, fmt.Errorf("reset-priority takes duration[,week|month|day|short|any[,routing|soonest]] or off")
	}
	d, err := time.ParseDuration(parts[0])
	if err != nil {
		return nil, fmt.Errorf("reset priority duration: %w", err)
	}
	var r ResetPriority
	r.Hours, r.Window = d.Hours(), "week"
	if len(parts) > 1 && parts[1] != "" {
		r.Window = parts[1]
	}
	if len(parts) > 2 && parts[2] != "routing" {
		r.Order = parts[2]
	}
	if err := r.Check(); err != nil {
		return nil, err
	}
	if r.Hours == 0 {
		return nil, nil
	}
	return &r, nil
}

// Check validates a reset priority before any provider settings are written.
func (r ResetPriority) Check() error {
	if math.IsNaN(r.Hours) || math.IsInf(r.Hours, 0) || r.Hours < 0 || r.Hours > 8760 {
		return fmt.Errorf("reset priority hours must be between 0 and 8760")
	}
	switch r.Window {
	case "", "week", "month", "day", "short", "any":
	default:
		return fmt.Errorf("reset priority window must be week, month, day, short or any")
	}
	if r.Order != "" && r.Order != "soonest" {
		return fmt.Errorf("reset priority order must be empty (current routing) or soonest")
	}
	return nil
}

// Due is the earliest matching reset inside the priority window. Only actual
// future resets with quota left count; missing spans never imply a week, and
// a passed reset is not a prediction of the next one. The comparison is a
// duration, independent of calendar dates and this computer's time zone.
func (r ResetPriority) Due(a Allowance, model string, now time.Time) time.Time {
	var due time.Time
	if r.Hours <= 0 || r.Check() != nil {
		return due
	}
	model = strings.ToLower(model)
	for _, l := range a {
		if !l.applies(model) || l.Used >= 100 || !l.Resets.After(now) || l.Resets.Sub(now).Hours() > r.Hours {
			continue
		}
		matches := false
		switch r.Window {
		case "", "week":
			matches = l.Span == 7*24*time.Hour
		case "month":
			matches = l.Span >= 28*24*time.Hour && l.Span <= 31*24*time.Hour
		case "day":
			matches = l.Span == 24*time.Hour
		case "short":
			matches = l.Span > 0 && l.Span < 24*time.Hour
		case "any":
			matches = true
		}
		if matches && (due.IsZero() || l.Resets.Before(due)) {
			due = l.Resets
		}
	}
	return due
}
