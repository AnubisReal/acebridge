package app

import (
	"net/http"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// Keep the redesigned shell compatible with the existing channel, source,
// playback and EPG controllers. These resources are served from the binary.
func TestWorkspaceDocumentContract(t *testing.T) {
	page, err := embeddedWeb.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := html.Parse(strings.NewReader(string(page)))
	if err != nil {
		t.Fatal(err)
	}
	ids := make(map[string]*html.Node)
	views := make(map[string]bool)
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		for _, attr := range node.Attr {
			if attr.Key == "id" {
				if ids[attr.Val] != nil {
					t.Errorf("duplicate HTML id: %s", attr.Val)
				}
				ids[attr.Val] = node
			}
			if attr.Key == "data-view" {
				views[attr.Val] = true
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(doc)
	for view := range views {
		if ids["view-"+view] == nil {
			t.Errorf("navigation has no matching view: %s", view)
		}
	}
	for _, id := range []string{
		"main-content", "navigation", "page-title", "page-subtitle",
		"channel-list", "channel-search", "channel-status", "channel-pagination",
		"bulk-channel-bar", "quick-channel-list", "quick-channel-search",
		"source-form", "source-use-proxy", "source-proxy-url", "source-edit-form",
		"settings-form", "epg-form", "epg-use-proxy", "epg-proxy-url",
		"playlist-url", "epg-output-url", "channel-edit-form", "diagnostic-dialog",
		"player-dialog", "channel-player", "open-stream", "monitor-stream-list",
		"overview-stream-list", "overview-epg-status", "overview-playlist-url",
		"overview-copy-playlist", "mobile-theme-toggle",
	} {
		if ids[id] == nil {
			t.Errorf("missing controller hook: #%s", id)
		}
	}
	for _, id := range []string{"channel-search", "quick-channel-search"} {
		found := false
		if node := ids[id]; node != nil {
			for _, attr := range node.Attr {
				if attr.Key == "aria-label" && attr.Val != "" {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("search lacks an accessible name: #%s", id)
		}
	}
}

func TestWorkspaceAssetsServed(t *testing.T) {
	handler := testApp(t).Routes()
	for _, asset := range []struct{ path, contentType, marker string }{
		{"/workspace.css", "text/css", ".overview-workspace"},
		{"/workspace.js", "javascript", "refreshOverview"},
		{"/app.js", "javascript", "acebridge:viewchange"},
	} {
		response := perform(handler, http.MethodGet, asset.path, nil, "")
		if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Type"), asset.contentType) || !strings.Contains(response.Body.String(), asset.marker) {
			t.Errorf("asset not served correctly: %s (%d, %s)", asset.path, response.Code, response.Header().Get("Content-Type"))
		}
	}
}
