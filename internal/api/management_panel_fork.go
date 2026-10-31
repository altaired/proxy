package api

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// panelLogoSVG is the fork logo shown in the management panel.
//
//go:embed assets/panel_logo.svg
var panelLogoSVG []byte

var (
	panelHeadClose   = []byte("</head>")
	panelLogoDataURI = "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(panelLogoSVG)

	// panelLogoScript points the panel favicon and its logo images (sidebar, login,
	// splash, About page) at the fork logo. The panel sets these from minified bundle
	// variables at startup and on re-render, so the elements are matched by their
	// readable class names and re-pointed whenever the app changes them.
	panelLogoScript = []byte(`<script>(()=>{const src="` + panelLogoDataURI + `";` +
		`const sel="img.sidebar-brand-logo,img[class*=splashLogo],img[class*=aboutLogo],img[class*=brandLogo]";` +
		`const swap=()=>{for(const img of document.querySelectorAll(sel))if(img.getAttribute("src")!==src)img.setAttribute("src",src);` +
		`for(const icon of document.querySelectorAll('link[rel="icon"]')){if(icon.getAttribute("href")!==src)icon.setAttribute("href",src);` +
		`if(icon.getAttribute("type")!=="image/svg+xml")icon.setAttribute("type","image/svg+xml")}};` +
		`new MutationObserver(swap).observe(document.documentElement,{childList:true,subtree:true,attributes:true,attributeFilter:["src","href","type"]});swap()})()</script>`)

	// panelQuotaAwareStyle hides the Routing page's "Within a tier" strategy picker
	// while routing.strategy is quota-aware. The panel only knows the built-in
	// strategies, so it would show round-robin and could save it back; the rest of
	// the page (priority tiers, session affinity, retries) still applies.
	panelQuotaAwareStyle = []byte(`<style>[data-routing-policy-column] section:has([class*=strategyGroup]){display:none}</style>`)
)

// serveForkManagementPanel serves the downloaded upstream management panel with the
// fork customizations injected at serve time, so panel auto-updates keep working
// without a forked panel. It skips conditional responses: the file's modification
// time does not change when the injected content does, so a 304 could keep serving
// a stale page.
func serveForkManagementPanel(c *gin.Context, filePath, routingStrategy string) {
	html, err := os.ReadFile(filePath)
	if err != nil {
		log.WithError(err).Error("failed to read management control panel asset")
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", withForkPanel(html, routingStrategy))
}

// withForkPanel injects the logo script, and the quota-aware style when that strategy
// is configured, at the end of <head>. The panel inlines its JS bundle in <head>, so
// the last </head> is the real one.
func withForkPanel(html []byte, routingStrategy string) []byte {
	head := bytes.LastIndex(html, panelHeadClose)
	if head < 0 {
		return html
	}
	out := make([]byte, 0, len(html)+len(panelLogoScript)+len(panelQuotaAwareStyle))
	out = append(out, html[:head]...)
	out = append(out, panelLogoScript...)
	if strings.EqualFold(strings.TrimSpace(routingStrategy), "quota-aware") {
		out = append(out, panelQuotaAwareStyle...)
	}
	return append(out, html[head:]...)
}
