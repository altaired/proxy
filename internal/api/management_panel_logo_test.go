package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWithPanelLogo(t *testing.T) {
	t.Parallel()
	favicon := `<link rel="icon" type="image/svg+xml" href="` + panelLogoDataURI + `">`
	// The panel inlines its JS bundle in <head>; strings inside it must stay untouched.
	bundle := `<script>const a='<link rel="icon" href="x">',b='</head>';</script>`

	tests := []struct {
		name string
		html string
		want string
	}{
		{
			name: "replaces the favicon and injects the logo script",
			html: `<head><link rel="icon" href="vite.svg">` + bundle + `</head><body></body>`,
			want: `<head>` + favicon + bundle + string(panelLogoScript) + `</head><body></body>`,
		},
		{
			name: "adds a favicon when the panel has none",
			html: `<head>` + bundle + `</head><body></body>`,
			want: `<head>` + bundle + favicon + string(panelLogoScript) + `</head><body></body>`,
		},
		{
			name: "leaves a panel without a head unchanged",
			html: `<html>management app</html>`,
			want: `<html>management app</html>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := string(withPanelLogo([]byte(tt.html))); got != tt.want {
				t.Fatalf("withPanelLogo() =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestManagementPanelServesForkLogo(t *testing.T) {
	staticDir := t.TempDir()
	t.Setenv("MANAGEMENT_STATIC_PATH", staticDir)
	panel := `<html><head><link rel="icon" href="vite.svg"></head><body><div id="root"></div></body></html>`
	if err := os.WriteFile(filepath.Join(staticDir, "management.html"), []byte(panel), 0o600); err != nil {
		t.Fatalf("failed to write management asset: %v", err)
	}
	server := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/management.html", nil)
	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	if strings.Contains(body, "vite.svg") || !strings.Contains(body, panelLogoDataURI) {
		t.Fatalf("panel was served without the fork logo: %s", body)
	}
}
