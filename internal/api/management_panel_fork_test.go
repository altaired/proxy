package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	proxyconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

func TestWithForkPanel(t *testing.T) {
	t.Parallel()
	// The panel inlines its JS bundle in <head>; a </head> inside it is not markup.
	bundle := `<script>const b='</head>';</script>`
	panel := `<head><link rel="icon" href="vite.svg">` + bundle + `</head><body></body>`
	head := `<head><link rel="icon" href="vite.svg">` + bundle

	tests := []struct {
		name     string
		html     string
		strategy string
		want     string
	}{
		{
			name:     "injects the logo script before the real end of head",
			html:     panel,
			strategy: "round-robin",
			want:     head + string(panelLogoScript) + `</head><body></body>`,
		},
		{
			name:     "hides the strategy picker for quota-aware routing",
			html:     panel,
			strategy: " Quota-Aware ",
			want:     head + string(panelLogoScript) + string(panelQuotaAwareStyle) + `</head><body></body>`,
		},
		{
			name:     "leaves a panel without a head unchanged",
			html:     `<html>management app</html>`,
			strategy: "quota-aware",
			want:     `<html>management app</html>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := string(withForkPanel([]byte(tt.html), tt.strategy)); got != tt.want {
				t.Fatalf("withForkPanel() =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestManagementPanelServesForkCustomizations(t *testing.T) {
	staticDir := t.TempDir()
	t.Setenv("MANAGEMENT_STATIC_PATH", staticDir)
	panel := `<html><head><link rel="icon" href="vite.svg"></head><body><div id="root"></div></body></html>`
	if err := os.WriteFile(filepath.Join(staticDir, "management.html"), []byte(panel), 0o600); err != nil {
		t.Fatalf("failed to write management asset: %v", err)
	}
	cfg := &proxyconfig.Config{SDKConfig: sdkconfig.SDKConfig{APIKeys: []string{"test-key"}}}
	cfg.Routing.Strategy = "quota-aware"
	server := newTestServerWithConfig(t, cfg)

	req := httptest.NewRequest(http.MethodGet, "/management.html", nil)
	// A browser revalidating a page cached before the customizations changed must
	// still get the full page, even though the panel file itself has not changed.
	req.Header.Set("If-Modified-Since", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat))
	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if body := rr.Body.String(); !strings.Contains(body, string(panelLogoScript)+string(panelQuotaAwareStyle)+"</head>") {
		t.Fatalf("panel was served without the fork customizations: %s", body)
	}
}
