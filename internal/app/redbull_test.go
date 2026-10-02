package app

import (
	"context"
	"io"
	"net/http"
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
