package api

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"net/http"
	"os"
	"regexp"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// panelLogoSVG is the fork logo shown in the management panel.
//
//go:embed assets/panel_logo.svg
var panelLogoSVG []byte

var (
	panelFaviconPattern = regexp.MustCompile(`<link\s[^>]*rel="icon"[^>]*>`)
	panelHeadClose      = []byte("</head>")
	panelScriptOpen     = []byte("<script")
	panelLogoDataURI    = "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(panelLogoSVG)

	// panelLogoScript swaps the panel's logo images (sidebar, login splash, About page)
	// for the fork logo. The images get their src from minified bundle variables, so
	// they are matched by their readable class names instead, and re-swapped whenever
	// the app re-renders them.
	panelLogoScript = []byte(`<script>(()=>{const src="` + panelLogoDataURI + `";` +
		`const sel="img.sidebar-brand-logo,img[class*=splashLogo],img[class*=aboutLogo]";` +
		`const swap=()=>{for(const img of document.querySelectorAll(sel))if(img.getAttribute("src")!==src)img.setAttribute("src",src)};` +
		`new MutationObserver(swap).observe(document.documentElement,{childList:true,subtree:true,attributes:true,attributeFilter:["src"]});swap()})()</script>`)
)

// serveManagementPanelWithLogo serves the downloaded upstream management panel with
// the fork logo swapped in at serve time, so panel auto-updates keep working without
// a forked panel. It skips conditional responses: the file's modification time does
// not change when the logo does, so a 304 could keep serving a cached stock page.
func serveManagementPanelWithLogo(c *gin.Context, filePath string) {
	html, err := os.ReadFile(filePath)
	if err != nil {
		log.WithError(err).Error("failed to read management control panel asset")
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", withPanelLogo(html))
}

// withPanelLogo replaces the panel favicon and injects the logo swap script. The
// panel inlines its whole JS bundle in <head>, so only an icon link before the first
// <script> and the last </head> (which follows the bundle) are real markup.
func withPanelLogo(html []byte) []byte {
	head := bytes.LastIndex(html, panelHeadClose)
	if head < 0 {
		return html
	}
	favicon := []byte(`<link rel="icon" type="image/svg+xml" href="` + panelLogoDataURI + `">`)
	out := make([]byte, 0, len(html)+len(favicon)+len(panelLogoScript))
	markup := html[:head]
	if script := bytes.Index(markup, panelScriptOpen); script >= 0 {
		markup = markup[:script]
	}
	if loc := panelFaviconPattern.FindIndex(markup); loc != nil {
		out = append(out, html[:loc[0]]...)
		out = append(out, favicon...)
		out = append(out, html[loc[1]:head]...)
	} else {
		out = append(out, html[:head]...)
		out = append(out, favicon...)
	}
	out = append(out, panelLogoScript...)
	return append(out, html[head:]...)
}
