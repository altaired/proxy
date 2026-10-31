package auth

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	internallogging "github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

// claudeSubscription builds a Claude OAuth credential with an observed quota snapshot.
// A negative weeklyUsed leaves the credential unobserved.
func claudeSubscription(id string, weeklyUsed float64, weeklyReset time.Time, fiveHourUsed float64) *Auth {
	auth := &Auth{ID: id, Provider: "claude", Metadata: map[string]any{"access_token": "token-" + id}}
	if weeklyUsed >= 0 {
		auth.Quota.ObservedAt = time.Now()
		auth.Quota.Signals = claudeQuotaHeaders(weeklyUsed, weeklyReset, fiveHourUsed)
	}
	return auth
}

func claudeQuotaHeaders(weeklyUsed float64, weeklyReset time.Time, fiveHourUsed float64) map[string]string {
	return map[string]string{
		"Anthropic-Ratelimit-Unified-7d-Utilization": strconv.FormatFloat(weeklyUsed, 'f', 2, 64),
		"Anthropic-Ratelimit-Unified-7d-Reset":       strconv.FormatInt(weeklyReset.Unix(), 10),
		"Anthropic-Ratelimit-Unified-5h-Utilization": strconv.FormatFloat(fiveHourUsed, 'f', 2, 64),
		"Anthropic-Ratelimit-Unified-5h-Reset":       strconv.FormatInt(time.Now().Add(2*time.Hour).Unix(), 10),
	}
}

func TestQuotaAwareSelector_Pick(t *testing.T) {
	t.Parallel()
	now := time.Now()
	apiKey := &Auth{ID: "0-api-key", Provider: "claude", Attributes: map[string]string{"api_key": "sk-test"}}

	tests := []struct {
		name  string
		auths []*Auth
		want  string
	}{
		{
			name: "prefers the subscription whose weekly quota resets soonest",
			auths: []*Auth{
				claudeSubscription("a-resets-next-week", 0.10, now.Add(6*24*time.Hour), 0),
				claudeSubscription("b-resets-tonight", 0.40, now.Add(3*time.Hour), 0),
			},
			want: "b-resets-tonight",
		},
		{
			name: "probes an unobserved subscription before ranking known ones",
			auths: []*Auth{
				claudeSubscription("a-known", 0.10, now.Add(3*time.Hour), 0),
				claudeSubscription("b-unobserved", -1, time.Time{}, 0),
			},
			want: "b-unobserved",
		},
		{
			name: "skips a subscription with an exhausted window",
			auths: []*Auth{
				claudeSubscription("a-5h-exhausted", 0.40, now.Add(3*time.Hour), 1.0),
				claudeSubscription("b-headroom", 0.10, now.Add(5*24*time.Hour), 0.2),
			},
			want: "b-headroom",
		},
		{
			name: "rolls a passed weekly reset forward to the next one",
			auths: []*Auth{
				// Reset passed a day ago, so the next reset is about six days out.
				claudeSubscription("a-stale", 0.95, now.Add(-24*time.Hour), 0),
				claudeSubscription("b-two-days", 0.50, now.Add(2*24*time.Hour), 0),
			},
			want: "b-two-days",
		},
		{
			name: "uses subscriptions with headroom before API keys",
			auths: []*Auth{
				apiKey,
				claudeSubscription("b-sub", 0.50, now.Add(5*24*time.Hour), 0),
			},
			want: "b-sub",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := (&QuotaAwareSelector{}).Pick(context.Background(), "claude", "", cliproxyexecutor.Options{}, tt.auths)
			if err != nil {
				t.Fatalf("Pick() error = %v", err)
			}
			if got.ID != tt.want {
				t.Fatalf("Pick() = %q, want %q", got.ID, tt.want)
			}
		})
	}
}

func TestQuotaAwareSelector_RoundRobinsCredentialsWithoutResetTime(t *testing.T) {
	t.Parallel()
	known := claudeSubscription("c-known", 0.10, time.Now().Add(3*time.Hour), 0)
	tests := []struct {
		name     string
		provider string
		auths    []*Auth
	}{
		{
			name:     "credentials without quota data",
			provider: "gemini",
			auths:    []*Auth{{ID: "a", Provider: "gemini"}, {ID: "b", Provider: "gemini"}},
		},
		{
			// Concurrent traffic after a restart must not pin every new session to one account.
			name:     "unobserved subscriptions",
			provider: "claude",
			auths:    []*Auth{claudeSubscription("a", -1, time.Time{}, 0), claudeSubscription("b", -1, time.Time{}, 0), known},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			selector := &QuotaAwareSelector{}
			var got []string
			for range 3 {
				picked, err := selector.Pick(context.Background(), tt.provider, "", cliproxyexecutor.Options{}, tt.auths)
				if err != nil {
					t.Fatalf("Pick() error = %v", err)
				}
				got = append(got, picked.ID)
			}
			if want := []string{"a", "b", "a"}; got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
				t.Fatalf("Pick() sequence = %v, want %v", got, want)
			}
		})
	}
}

func TestQuotaRoutingRankFor_CodexWindows(t *testing.T) {
	t.Parallel()
	observedAt := time.Now().Add(-time.Hour)
	auth := &Auth{ID: "codex", Provider: "codex", Metadata: map[string]any{"access_token": "token"}}
	auth.Quota.ObservedAt = observedAt
	auth.Quota.Signals = map[string]string{
		"X-Codex-Primary-Used-Percent":          "20",
		"X-Codex-Primary-Window-Minutes":        "300",
		"X-Codex-Primary-Reset-After-Seconds":   "7200",
		"X-Codex-Secondary-Used-Percent":        "35.5",
		"X-Codex-Secondary-Window-Minutes":      "10080",
		"X-Codex-Secondary-Reset-After-Seconds": "86400",
	}

	rank := quotaRoutingRankFor(auth, time.Now())
	if rank.group != quotaGroupHeadroom {
		t.Fatalf("group = %v, want headroom", rank.group)
	}
	// The weekly (secondary) window decides, measured from when the snapshot was taken.
	if want := observedAt.Add(24 * time.Hour); !rank.resetAt.Equal(want) {
		t.Fatalf("resetAt = %v, want %v", rank.resetAt, want)
	}

	auth.Quota.Signals["X-Codex-Primary-Used-Percent"] = "100"
	if rank := quotaRoutingRankFor(auth, time.Now()); rank.group != quotaGroupExhausted {
		t.Fatalf("group with exhausted primary window = %v, want exhausted", rank.group)
	}
}

func TestQuotaAwareSelector_SessionAffinityKeepsBoundSubscription(t *testing.T) {
	t.Parallel()
	now := time.Now()
	selector := NewSessionAffinitySelector(&QuotaAwareSelector{})
	defer selector.Stop()

	later := claudeSubscription("a-later", 0.10, now.Add(5*24*time.Hour), 0)
	sooner := claudeSubscription("b-sooner", 0.10, now.Add(4*24*time.Hour), 0)
	sessionOpts := func(id string) cliproxyexecutor.Options {
		return cliproxyexecutor.Options{Headers: http.Header{"X-Claude-Code-Session-Id": []string{id}}}
	}

	first, err := selector.Pick(context.Background(), "claude", "claude-sonnet", sessionOpts("session-1"), []*Auth{later, sooner})
	if err != nil || first.ID != "b-sooner" {
		t.Fatalf("first Pick() = (%v, %v), want b-sooner", first, err)
	}

	// Another subscription becomes more urgent; the running session keeps its cache-warm account.
	urgent := claudeSubscription("c-urgent", 0.10, now.Add(time.Hour), 0)
	auths := []*Auth{later, sooner, urgent}
	got, err := selector.Pick(context.Background(), "claude", "claude-sonnet", sessionOpts("session-1"), auths)
	if err != nil || got.ID != "b-sooner" {
		t.Fatalf("bound session Pick() = (%v, %v), want b-sooner", got, err)
	}
	got, err = selector.Pick(context.Background(), "claude", "claude-sonnet", sessionOpts("session-2"), auths)
	if err != nil || got.ID != "c-urgent" {
		t.Fatalf("new session Pick() = (%v, %v), want c-urgent", got, err)
	}
}

// The Manager feeds each response's quota headers into the next selection.
func TestManager_QuotaAwarePicksFromLatestObservation(t *testing.T) {
	now := time.Now()
	manager := NewManager(nil, &QuotaAwareSelector{}, nil)
	manager.RegisterExecutor(schedulerProviderTestExecutor{provider: "claude"})
	registerSchedulerModels(t, "claude", "claude-sonnet", "quota-a", "quota-b")
	for _, auth := range []*Auth{
		claudeSubscription("quota-a", 0.10, now.Add(2*24*time.Hour), 0),
		claudeSubscription("quota-b", 0.10, now.Add(5*24*time.Hour), 0),
	} {
		auth.Status = StatusActive
		if _, err := manager.Register(WithSkipPersist(context.Background()), auth); err != nil {
			t.Fatalf("Register(%s) error = %v", auth.ID, err)
		}
	}
	pick := func() string {
		t.Helper()
		picked, _, err := manager.pickNext(context.Background(), "claude", "claude-sonnet", cliproxyexecutor.Options{}, nil)
		if err != nil {
			t.Fatalf("pickNext() error = %v", err)
		}
		return picked.ID
	}
	if got := pick(); got != "quota-a" {
		t.Fatalf("pickNext() = %q, want quota-a (soonest weekly reset)", got)
	}

	// A successful response reports quota-a's 5h window as used up.
	ctx := internallogging.WithResponseHeadersHolder(context.Background())
	headers := http.Header{}
	for name, value := range claudeQuotaHeaders(0.30, now.Add(2*24*time.Hour), 1.0) {
		headers.Set(name, value)
	}
	internallogging.SetResponseHeaders(ctx, headers)
	manager.MarkResult(ctx, Result{AuthID: "quota-a", Provider: "claude", Model: "claude-sonnet", Success: true})

	if got := pick(); got != "quota-b" {
		t.Fatalf("pickNext() after exhaustion = %q, want quota-b", got)
	}
}
