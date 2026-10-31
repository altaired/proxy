package auth

import (
	"context"
	"math"
	"strconv"
	"strings"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

// QuotaAwareSelector spends subscription quota that is about to reset before quota
// that will still be there later. Among subscription credentials with headroom it
// picks the one whose longest quota window (the weekly limit) resets soonest, which
// drains it before moving on and keeps new sessions on one account. Credentials it
// has never observed are tried first so a single response teaches it their windows.
// Credentials without quota data (API keys, providers that do not report usage)
// keep round-robin behavior.
type QuotaAwareSelector struct {
	roundRobin RoundRobinSelector
}

// quotaRoutingGroup orders credentials for quota-aware routing; lower groups win.
type quotaRoutingGroup int

const (
	// quotaGroupUnobserved is a subscription the proxy has no usage snapshot for yet.
	quotaGroupUnobserved quotaRoutingGroup = iota
	// quotaGroupHeadroom is a subscription with remaining quota in every window.
	quotaGroupHeadroom
	// quotaGroupNoData is a credential without usable quota data.
	quotaGroupNoData
	// quotaGroupExhausted is a subscription whose snapshot shows a used-up window.
	quotaGroupExhausted
)

type quotaRoutingRank struct {
	group quotaRoutingGroup
	// resetAt is when the longest quota window resets; set only for quotaGroupHeadroom.
	resetAt time.Time
}

// quotaWindow is one provider usage window normalized from observed quota signals.
type quotaWindow struct {
	remaining float64 // fraction of the window still available, 0..1
	resetAt   time.Time
	length    time.Duration
}

// Pick selects the available auth with the most urgent quota, falling back to
// round-robin among credentials that rank equally without a reset time.
func (s *QuotaAwareSelector) Pick(ctx context.Context, provider, model string, opts cliproxyexecutor.Options, auths []*Auth) (*Auth, error) {
	now := time.Now()
	available, err := getSelectorAvailableAuths(ctx, auths, provider, model, now)
	if err != nil {
		return nil, err
	}
	available = preferCodexWebsocketAuths(ctx, provider, available)

	ranks := make([]quotaRoutingRank, len(available))
	best := -1
	for i, candidate := range available {
		ranks[i] = quotaRoutingRankFor(candidate, now)
		if best < 0 || ranks[i].less(ranks[best], candidate.ID, available[best].ID) {
			best = i
		}
	}
	if ranks[best].group <= quotaGroupHeadroom {
		return available[best], nil
	}
	tied := make([]*Auth, 0, len(available))
	for i, candidate := range available {
		if ranks[i].group == ranks[best].group {
			tied = append(tied, candidate)
		}
	}
	return s.roundRobin.Pick(ctx, provider, model, opts, tied)
}

// pickQuotaAwareLocked is the scheduler counterpart of QuotaAwareSelector.Pick.
func (m *modelScheduler) pickQuotaAwareLocked(view *readyView, predicate func(*scheduledAuth) bool) *scheduledAuth {
	rankOf := m.quotaRankFunc(time.Now())
	best, bestRank := bestQuotaAwareEntry(view.flat, predicate, rankOf)
	if best == nil || bestRank.group <= quotaGroupHeadroom {
		return best
	}
	return view.pickRoundRobin(func(entry *scheduledAuth) bool {
		if predicate != nil && !predicate(entry) {
			return false
		}
		return rankOf(entry).group == bestRank.group
	})
}

// quotaRankFunc ranks shard entries from the provider-level auth snapshot. Results
// scoped to one model refresh only that model's shard, so an entry's own snapshot
// can carry an older quota observation than the provider-level one.
func (m *modelScheduler) quotaRankFunc(now time.Time) func(*scheduledAuth) quotaRoutingRank {
	return func(entry *scheduledAuth) quotaRoutingRank {
		auth := entry.auth
		if meta := m.authMetas[auth.ID]; meta != nil && meta.auth != nil {
			auth = meta.auth
		}
		return quotaRoutingRankFor(auth, now)
	}
}

// bestQuotaAwareEntry returns the highest-ranked entry matching predicate.
func bestQuotaAwareEntry(entries []*scheduledAuth, predicate func(*scheduledAuth) bool, rankOf func(*scheduledAuth) quotaRoutingRank) (*scheduledAuth, quotaRoutingRank) {
	var best *scheduledAuth
	var bestRank quotaRoutingRank
	for _, entry := range entries {
		if entry == nil || entry.auth == nil || (predicate != nil && !predicate(entry)) {
			continue
		}
		rank := rankOf(entry)
		if best == nil || rank.less(bestRank, entry.auth.ID, best.auth.ID) {
			best, bestRank = entry, rank
		}
	}
	return best, bestRank
}

func (r quotaRoutingRank) less(other quotaRoutingRank, id, otherID string) bool {
	if r.group != other.group {
		return r.group < other.group
	}
	if !r.resetAt.Equal(other.resetAt) {
		return r.resetAt.Before(other.resetAt)
	}
	return id < otherID
}

// quotaRoutingRankFor classifies auth from its last observed quota snapshot.
func quotaRoutingRankFor(auth *Auth, now time.Time) quotaRoutingRank {
	if auth == nil || auth.AuthKind() != AuthKindOAuth {
		return quotaRoutingRank{group: quotaGroupNoData}
	}
	provider := strings.ToLower(strings.TrimSpace(auth.Provider))
	if provider != "claude" && provider != "codex" {
		return quotaRoutingRank{group: quotaGroupNoData}
	}
	if auth.Quota.ObservedAt.IsZero() {
		return quotaRoutingRank{group: quotaGroupUnobserved}
	}
	windows := observedQuotaWindows(provider, auth.Quota, now)
	if len(windows) == 0 {
		return quotaRoutingRank{group: quotaGroupNoData}
	}
	longest := windows[0]
	for _, window := range windows {
		if window.remaining <= 0 {
			return quotaRoutingRank{group: quotaGroupExhausted}
		}
		if window.length > longest.length {
			longest = window
		}
	}
	return quotaRoutingRank{group: quotaGroupHeadroom, resetAt: longest.resetAt}
}

// observedQuotaWindows parses the Claude unified 5h/7d windows or the Codex
// primary/secondary windows from a quota snapshot. A window whose reset has
// already passed is reported as fully available and rolled forward to its
// next estimated reset, since the snapshot predates the reset.
func observedQuotaWindows(provider string, quota QuotaState, now time.Time) []quotaWindow {
	var windows []quotaWindow
	add := func(used float64, resetAt time.Time, length time.Duration) {
		if math.IsNaN(used) || used < 0 || resetAt.IsZero() || length <= 0 {
			return
		}
		window := quotaWindow{remaining: max(0, 1-used), resetAt: resetAt, length: length}
		if !window.resetAt.After(now) {
			periods := now.Sub(window.resetAt)/window.length + 1
			window.resetAt = window.resetAt.Add(periods * window.length)
			window.remaining = 1
		}
		windows = append(windows, window)
	}

	signals := quota.Signals
	switch provider {
	case "claude":
		for _, w := range []struct {
			name   string
			length time.Duration
		}{{"5h", 5 * time.Hour}, {"7d", 7 * 24 * time.Hour}} {
			prefix := "Anthropic-Ratelimit-Unified-" + w.name + "-"
			used, okUsed := parseQuotaFloat(signals[prefix+"Utilization"])
			resetAt, okReset := parseQuotaUnix(signals[prefix+"Reset"])
			if okUsed && okReset {
				add(used, resetAt, w.length)
			}
		}
	case "codex":
		for _, name := range []string{"Primary", "Secondary"} {
			prefix := "X-Codex-" + name + "-"
			usedPercent, okUsed := parseQuotaFloat(signals[prefix+"Used-Percent"])
			minutes, okMinutes := parseQuotaFloat(signals[prefix+"Window-Minutes"])
			if !okUsed || !okMinutes {
				continue
			}
			resetAt, okReset := parseQuotaUnix(signals[prefix+"Reset-At"])
			if !okReset {
				// Reset-After-Seconds is relative to when the snapshot was taken.
				if seconds, okAfter := parseQuotaFloat(signals[prefix+"Reset-After-Seconds"]); okAfter && seconds >= 0 {
					resetAt, okReset = quota.ObservedAt.Add(time.Duration(seconds*float64(time.Second))), true
				}
			}
			if okReset {
				add(usedPercent/100, resetAt, time.Duration(minutes*float64(time.Minute)))
			}
		}
	}
	return windows
}

func parseQuotaFloat(raw string) (float64, bool) {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return value, true
}

func parseQuotaUnix(raw string) (time.Time, bool) {
	seconds, ok := parseQuotaFloat(raw)
	if !ok || seconds <= 0 {
		return time.Time{}, false
	}
	return time.Unix(int64(seconds), 0), true
}
