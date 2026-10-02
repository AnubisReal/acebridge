package app

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestFetchRedBullPadelChannelsUsesLiveEventPlayURL(t *testing.T) {
	a := testApp(t)
	defer a.Close()
	const eventID = "rrn:content:event-profiles:039a06f9-d5fc-445a-ba9a-d6a6bcc5ccf0"
	const liveVideoID = "rrn:content:live-videos:a228b1bc-a151-40c0-b4ef-71727027a24d"
	a.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var body string
		switch {
		case request.URL.Path == "/v3/session":
			body = `{"token":"test-token"}`
		case request.URL.Path == "/v3/products/"+eventID:
			body = `{"id":"` + eventID + `","title":"Premier Padel","subheading":"Rotterdam Premier Padel P2","status":{"code":"live","play":"` + liveVideoID + `"},"collections":[{"id":"` + eventID + `:recommendations:same_type"}]}`
		default:
			t.Fatalf("unexpected Red Bull request: %s", request.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: request}, nil
	})}

	channels, err := a.fetchRedBullPadelChannels(context.Background(), "https://www.redbull.tv/es_ES/partner-channel/"+eventID)
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) != 1 {
		t.Fatalf("got %d channels, want 1", len(channels))
	}
	channel := channels[0]
	if channel.AceStreamID != liveVideoID || channel.TVGID != liveVideoID || channel.Name != "Rotterdam Premier Padel P2" || channel.Group != "Premier Padel" {
		t.Fatalf("unexpected direct live channel: %#v", channel)
	}
}

func TestRedBullPlaybackURLUsesCurrentPlaybackEndpoint(t *testing.T) {
	const liveVideoID = "rrn:content:live-videos:a228b1bc-a151-40c0-b4ef-71727027a24d"
	got, err := (&App{}).redBullPlaybackURL(context.Background(), liveVideoID)
	if err != nil {
		t.Fatal(err)
	}
	want := redBullPlaybackBaseURL + "/" + liveVideoID + ".m3u8?device_group=group_5"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestAllowedRedBullStreamURL(t *testing.T) {
	for _, raw := range []string{
		"https://play.redbull.com/main/v1/rbtv/es_ES/es/personal_computer/http/live.m3u8",
		"https://rbmn-live.akamaized.net/hls/live/123/master.m3u8",
	} {
		parsed, _ := url.Parse(raw)
		if !allowedRedBullStreamURL(parsed) {
			t.Fatalf("expected allowed Red Bull URL: %s", raw)
		}
	}
	parsed, _ := url.Parse("https://dms.redbull.tv/v3/legacy/playlist.m3u8")
	if allowedRedBullStreamURL(parsed) {
		t.Fatal("legacy Red Bull playback host must not be allowed")
	}
}

func TestRedBullStreamProxyUsesRedBullUserAgent(t *testing.T) {
	a := testApp(t)
	defer a.Close()
	_, err := a.db.Exec(`INSERT INTO sources(id,name,kind) VALUES (1,'Premier Padel','redbull_padel');
		INSERT INTO channels(id,source_id,name,acestream_id,source_acestream_id) VALUES (1,1,'Directo','rrn:content:live-videos:test','rrn:content:live-videos:test')`)
	if err != nil {
		t.Fatal(err)
	}
	a.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("User-Agent") != redBullUserAgent {
			t.Fatalf("got User-Agent %q, want %q", request.Header.Get("User-Agent"), redBullUserAgent)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("#EXTM3U\n")), Header: http.Header{"Content-Type": []string{"application/vnd.apple.mpegurl"}}, Request: request}, nil
	})}
	target := "https://play.redbull.com/main/v1/rbtv/es_ES/es/personal_computer/http/rrn:content:live-videos:test.m3u8?device_group=group_5"
	response := perform(a.Routes(), http.MethodGet, "/api/v1/channels/1/stream?url="+url.QueryEscape(target), nil, "")
	if response.Code != http.StatusOK {
		t.Fatalf("stream status %d: %s", response.Code, response.Body.String())
	}
}
