package app

import "testing"

func TestLogoCatalogMatchesNormalizedAndSimilarNames(t *testing.T) {
	catalog := buildLogoCatalog(
		[]iptvOrgChannel{
			{ID: "La1.es", Name: "La 1", AltNames: []string{"TVE La 1"}},
			{ID: "La2.es", Name: "La 2"},
		},
		[]iptvOrgLogo{
			{Channel: "La1.es", InUse: true, Format: "SVG", URL: "https://example.com/la1.svg"},
			{Channel: "La2.es", InUse: true, Format: "SVG", URL: "https://example.com/la2.svg"},
		},
	)

	for _, test := range []struct {
		tvgID string
		name  string
		want  string
	}{
		{"La1.es", "Cualquier nombre", "https://example.com/la1.svg"},
		{"", "La 1 HD", "https://example.com/la1.svg"},
		{"", "TVE La1", "https://example.com/la1.svg"},
	} {
		if got := catalog.match(test.tvgID, test.name); got != test.want {
			t.Fatalf("match(%q,%q)=%q, want %q", test.tvgID, test.name, got, test.want)
		}
	}
}

func TestLogoCatalogRejectsWeakOrAmbiguousMatches(t *testing.T) {
	catalog := buildLogoCatalog(
		[]iptvOrgChannel{
			{ID: "NewsOne.es", Name: "News One"},
			{ID: "NewsOn.es", Name: "News On"},
		},
		[]iptvOrgLogo{
			{Channel: "NewsOne.es", InUse: true, URL: "https://example.com/one.png"},
			{Channel: "NewsOn.es", InUse: true, URL: "https://example.com/on.png"},
		},
	)

	if got := catalog.match("", "News O"); got != "" {
		t.Fatalf("ambiguous match returned %q", got)
	}
	if got := catalog.match("", "Completely Different"); got != "" {
		t.Fatalf("weak match returned %q", got)
	}
}

func TestChooseLogoPrefersCurrentNonWhiteSVG(t *testing.T) {
	whiteFeed := "HD"
	got := chooseLogo([]iptvOrgLogo{
		{URL: "https://example.com/old.png", Width: 2000, Format: "PNG"},
		{URL: "https://example.com/white.svg", InUse: true, Feed: &whiteFeed, Tags: []string{"white"}, Width: 2000, Format: "SVG"},
		{URL: "https://example.com/current.svg", InUse: true, Tags: []string{"horizontal"}, Width: 1000, Format: "SVG"},
	})
	if got != "https://example.com/current.svg" {
		t.Fatalf("chooseLogo()=%q", got)
	}
}

func TestNormalizeChannelNameRemovesPresentationNoise(t *testing.T) {
	if got := normalizeChannelName("  CANAL Águila TV FHD  "); got != "aguila" {
		t.Fatalf("normalizeChannelName()=%q", got)
	}
}
