package gateway

import (
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/routes"
)

// resetPriorityOrder gives the chosen strategy only the preferred candidates
// first, so rotations and weights share requests within that pool. Ordinary
// routes remain behind it for failover. A missing strategy keeps listed order.
func resetPriorityOrder(p provider.Provider, st routes.Strategy, cs []candidate, seats []routes.Seat, wg weighing) []int {
	if p.ResetPriority == nil || p.ResetPriority.Hours <= 0 {
		return routes.Apply(st, p.ID, seats)
	}
	var preferred, ordinary []int
	for i, c := range cs {
		if wg.lefts[c.allowanceKey()].reset.IsZero() || seats[i].Resting {
			ordinary = append(ordinary, i)
		} else {
			preferred = append(preferred, i)
		}
	}
	order := func(pool string, indexes []int) []int {
		if len(indexes) < 2 {
			return indexes
		}
		sub := make([]routes.Seat, len(indexes))
		for i, k := range indexes {
			sub[i] = seats[k]
		}
		out := make([]int, 0, len(indexes))
		for _, i := range routes.Apply(st, pool, sub) {
			out = append(out, indexes[i])
		}
		return out
	}
	if len(preferred) == 0 {
		return order(p.ID, ordinary)
	}
	if p.ResetPriority.Order == "soonest" {
		sort.SliceStable(preferred, func(i, j int) bool {
			return wg.lefts[cs[preferred[i]].allowanceKey()].reset.Before(wg.lefts[cs[preferred[j]].allowanceKey()].reset)
		})
		// Equal reset times share their own round too. Ordering the whole
		// preferred pool first would let later resets distort that round.
		var ranked []int
		for start, group := 0, 0; start < len(preferred); group++ {
			end := start + 1
			at := wg.lefts[cs[preferred[start]].allowanceKey()].reset
			for end < len(preferred) && wg.lefts[cs[preferred[end]].allowanceKey()].reset.Equal(at) {
				end++
			}
			pool := p.ID
			if group > 0 {
				pool += "/reset-after/" + strconv.Itoa(group)
			}
			ranked = append(ranked, order(pool, preferred[start:end])...)
			start = end
		}
		preferred = ranked
	} else {
		preferred = order(p.ID, preferred)
	}
	return append(preferred, order(p.ID+"/reset-fallback", ordinary)...)
}

// resetPriorityBehind keeps automatic affinity and session spreading inside
// the leading provider's preferred pool. An explicit Session or Strict and a
// turn already under way still keep the account the user chose to stay on.
func resetPriorityBehind(order []Weighed, i int) bool {
	return i > 0 && i < len(order) && resetPrecedes(order[0], order[i])
}

// resetPreventsPromotion protects a member's reset queue when affinity or
// load sharing considers bringing one of its accounts to the front.
// The first seat in the group may belong to a different provider.
func resetPreventsPromotion(order []Weighed, i int) bool {
	if i <= 0 || i >= len(order) {
		return false
	}
	other := order[i]
	if resetPrecedes(order[0], other) {
		return true
	}
	return other.resetPolicy && slices.ContainsFunc(order[1:i], func(first Weighed) bool {
		return resetPrecedes(first, other)
	})
}

func resetPrecedes(first, other Weighed) bool {
	if first.ResetPriority == nil || first.Rest != nil || first.Aside ||
		first.Busy > 0 && other.Busy == 0 || first.CreditsLast && !other.CreditsLast ||
		first.Provider != other.Provider || first.Model != other.Model || first.Fixed != other.Fixed ||
		first.Fast != other.Fast || first.Fallback != other.Fallback || !slices.Equal(first.Via, other.Via) {
		return false
	}
	return other.ResetPriority == nil || first.ResetSoonest && other.ResetPriority.After(*first.ResetPriority)
}

func resetDue(p provider.Provider, a provider.Allowance, model string, now time.Time, used float64) time.Time {
	// Use the remaining quota until reset. The ordinary routing buffer
	// would leave the final 2% unused. A full window still blocks priority,
	// including when a different window is the one about to reset.
	if p.ResetPriority == nil || used >= 100 {
		return time.Time{}
	}
	return p.ResetPriority.Due(a, model, now)
}

// resetFirst preserves the priority after secondary ordering such as Sink
// and protocol fit. Those rules still order candidates in the same tier.
func resetFirst(p provider.Provider, cs []candidate, wg weighing) []candidate {
	if p.ResetPriority == nil || p.ResetPriority.Hours <= 0 {
		return cs
	}
	dues := make(map[allowanceKey]time.Time, len(cs))
	for _, c := range cs {
		if !resting(c) {
			dues[c.allowanceKey()] = wg.lefts[c.allowanceKey()].reset
		}
	}
	sort.SliceStable(cs, func(i, j int) bool {
		a, b := dues[cs[i].allowanceKey()], dues[cs[j].allowanceKey()]
		if a.IsZero() != b.IsZero() {
			return !a.IsZero()
		}
		return p.ResetPriority.Order == "soonest" && !a.IsZero() && a.Before(b)
	})
	return cs
}
