package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWithPanelLogo(t *testing.T) {
	t.Parallel()
	// The panel inlines its JS bundle in <head>; a </head> inside it is not markup.
	bundle := `<script>const b='</head>';</script>`

	tests := []struct {
		name string
		html string
		want string
	}{
		{
			name: "injects the logo script before the real end of head",
			html: `<head><link rel="icon" href="vite.svg">` + bundle + `</head><body></body>`,
			want: `<head><link rel="icon" href="vite.svg">` + bundle + string(panelLogoScript) + `</head><body></body>`,
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
	// A browser revalidating a page cached before the logo changed must still get
	// the full page, even though the panel file itself has not changed.
	req.Header.Set("If-Modified-Since", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat))
	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	if !strings.Contains(body, string(panelLogoScript)+"</head>") {
		t.Fatalf("panel was served without the fork logo: %s", body)
	}
}
