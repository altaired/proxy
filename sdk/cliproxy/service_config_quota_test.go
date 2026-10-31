package cliproxy

import (
	"testing"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func TestQuotaAwareRoutingSelector(t *testing.T) {
	state := normalizedRoutingRuntimeState(&internalconfig.Config{
		Routing: internalconfig.RoutingConfig{Strategy: "quota-aware"},
	})
	if state.strategy != "quota-aware" {
		t.Fatalf("strategy = %q, want quota-aware", state.strategy)
	}
	if _, ok := newRoutingSelector(state).(*coreauth.QuotaAwareSelector); !ok {
		t.Fatalf("selector type = %T, want *auth.QuotaAwareSelector", newRoutingSelector(state))
	}

}
