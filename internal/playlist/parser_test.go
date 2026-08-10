package playlist

import "testing"

func TestParse(t *testing.T) {
	input := `#EXTM3U
#EXTINF:-1 tvg-id="demo.es" tvg-name="Demo HD" group-title="General" tvg-logo="https://example.com/logo.png",Demo
acestream://0123456789abcdef0123456789abcdef01234567`
	channels := Parse(input)
	if len(channels) != 1 || channels[0].Name != "Demo HD" || channels[0].Group != "General" {
		t.Fatalf("unexpected channels: %#v", channels)
	}
}

func TestExtractAceStreamIDFromHTTP(t *testing.T) {
	want := "0123456789abcdef0123456789abcdef01234567"
	got := ExtractAceStreamID("http://engine:6878/ace/manifest.m3u8?id=" + want)
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExtractRawAceStreamID(t *testing.T) {
	want := "0123456789abcdef0123456789abcdef01234567"
	if got := ExtractAceStreamID(want); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestParseHashJSON(t *testing.T) {
	input := `{
		"generated":"2026-08-03T01:01:15Z",
		"count":2,
		"hashes":[
			{"title":"Canal Uno 1080p","hash":"0123456789abcdef0123456789abcdef01234567","group":"Deportes","logo":"https://example.com/uno.png","tvg_id":"canal.uno"},
			{"title":"Entrada inválida","hash":"no-es-un-hash"}
		]
	}`
	channels := Parse(input)
	if len(channels) != 1 {
		t.Fatalf("got %d channels, want 1: %#v", len(channels), channels)
	}
	got := channels[0]
	if got.Name != "Canal Uno 1080p" || got.Group != "Deportes" || got.Logo != "https://example.com/uno.png" || got.TVGID != "canal.uno" || got.AceStreamID != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("unexpected channel: %#v", got)
	}
}

func TestParseJSONChannelArray(t *testing.T) {
	input := `[{"name":"Canal directo","acestream_id":"abcdef0123456789abcdef0123456789abcdef01"}]`
	channels := Parse(input)
	if len(channels) != 1 || channels[0].Name != "Canal directo" {
		t.Fatalf("unexpected channels: %#v", channels)
	}
}

func TestParsePlainAlternatingList(t *testing.T) {
	input := `Canal 1 (1RFEF) (SOLO EVENTOS) --> NEW ERA V
c1ee6e1e0ef5928d8261f855c417de21794f437d

DAZN 1 --> SPORT TV
d5b2c6b940cf3df5e8f9dc6f000f0ea23a10b151`
	channels := Parse(input)
	if len(channels) != 2 {
		t.Fatalf("got %d channels, want 2: %#v", len(channels), channels)
	}
	if channels[0].Name != "Canal 1 (1RFEF) (SOLO EVENTOS) --> NEW ERA V" || channels[1].Name != "DAZN 1 --> SPORT TV" {
		t.Fatalf("unexpected names: %#v", channels)
	}
}

func TestParseExtendedM3UWithGroupDeclarations(t *testing.T) {
	input := `#EXTM3U url-tvg="https://example.com/epg.xml"
#EXTVLCOPT:network-caching=1000
#EXTGRP: group-title="DAZN" group-logo="https://example.com/group.png"
#EXTINF:-1 tvg-logo="https://example.com/dazn.png" tvg-id="DAZN 1 HD" group-title="DAZN", DAZN 1 --> SPORT TV
acestream://d5b2c6b940cf3df5e8f9dc6f000f0ea23a10b151`
	channels := Parse(input)
	if len(channels) != 1 || channels[0].Name != "DAZN 1 --> SPORT TV" || channels[0].Group != "DAZN" || channels[0].TVGID != "DAZN 1 HD" {
		t.Fatalf("unexpected channels: %#v", channels)
	}
}
