package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/anubisreal/acebridge/internal/playlist"
)

// Opt-in only: regular tests must remain deterministic and work offline.
func TestRedBullRemoteCatalog(t *testing.T) {
	if os.Getenv("ACEBRIDGE_TEST_REDBULL_REMOTE") != "1" {
		t.Skip("set ACEBRIDGE_TEST_REDBULL_REMOTE=1 to verify the public catalog")
	}
	a := testApp(t)
	channels, err := a.fetchRedBullPadelChannels(context.Background(), redBullEventsURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) == 0 {
		t.Fatal("no playable channels")
	}
	for _, channel := range channels {
		if channel.BroadcastState != "live" && channel.BroadcastState != "replay" {
			t.Fatalf("missing state: %#v", channel)
		}
		t.Logf("%s: %s", channel.Name, channel.BroadcastState)
	}
}

func TestRedBullReplaySyncClearsFalseErrorAndExposesState(t *testing.T) {
	a := testApp(t)
	defer a.Close()
	const event = "rrn:content:event-profiles:039a06f9-d5fc-445a-ba9a-d6a6bcc5ccf0"
	const video = "rrn:content:live-videos:final"
	_, err := a.db.Exec(`INSERT INTO sources(id,name,kind,url,last_status,last_error) VALUES (1,'Padel','redbull_padel',?,'error','no current Premier Padel event was found')`, redBullEventsURL)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = a.replaceChannels(context.Background(), 1, []playlist.Channel{{TVGID: video, Name: "Milan", AceStreamID: video}})
	if err != nil {
		t.Fatal(err)
	}
	phase := "replay"
	a.sourceClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := event
		if phase == "missing" {
			body = "rrn:content:event-profiles:039a06f9-d5fc-445a-ba9a-d6a6bcc5ccf2"
		}
		return redBullTestResponse(r, body), nil
	})}
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if phase == "network" {
			return nil, errors.New("upstream unavailable")
		}
		switch r.URL.Path {
		case "/v3/session":
			return redBullTestResponse(r, `{"token":"test"}`), nil
		case "/v3/products/" + event:
			if phase == "live" {
				return redBullTestResponse(r, `{"id":"`+event+`","title":"Premier Padel","subheading":"Kuwait","status":{"code":"live","play":"rrn:content:live-videos:new-final"}}`), nil
			}
			return redBullTestResponse(r, `{"id":"`+event+`","title":"Premier Padel","subheading":"Milan","status":{"code":"post","play":"`+video+`"}}`), nil
		case "/v3/products/" + video:
			return redBullTestResponse(r, `{"id":"`+video+`","title":"Final – Milano","playable":true,"status":{"code":"post"}}`), nil
		case "/v3/products/rrn:content:event-profiles:039a06f9-d5fc-445a-ba9a-d6a6bcc5ccf2":
			return redBullTestResponse(r, `{"title":"Other sport"}`), nil
		default:
			t.Fatalf("unexpected request %s", r.URL)
			return nil, nil
		}
	})}
	for _, mode := range []string{"replay", "missing"} {
		phase = mode
		if _, err := a.db.Exec(`UPDATE sources SET last_status='error',last_error='no current Premier Padel event was found' WHERE id=1`); err != nil {
			t.Fatal(err)
		}
		if err := a.syncSource(context.Background(), 1); err != nil {
			t.Fatal(err)
		}
		var status, message, broadcast string
		if err := a.db.QueryRow(`SELECT last_status,last_error FROM sources WHERE id=1`).Scan(&status, &message); err != nil {
			t.Fatal(err)
		}
		if err := a.db.QueryRow(`SELECT broadcast_state FROM channels WHERE source_id=1`).Scan(&broadcast); err != nil {
			t.Fatal(err)
		}
		if status != "healthy" || message != "" || broadcast != "replay" {
			t.Fatalf("%s: %s %q %s", mode, status, message, broadcast)
		}
		response := perform(a.Routes(), http.MethodGet, "/api/v1/channels", nil, "")
		if response.Code != 200 || !strings.Contains(response.Body.String(), `"broadcast_state":"replay"`) {
			t.Fatalf("missing replay state: %s", response.Body.String())
		}
		dashboard := perform(a.Routes(), http.MethodGet, "/api/v1/dashboard", nil, "")
		if dashboard.Code != 200 || !strings.Contains(dashboard.Body.String(), `"failed_sources":0`) {
			t.Fatalf("false dashboard warning: %s", dashboard.Body.String())
		}
	}
	phase = "network"
	if err := a.syncSource(context.Background(), 1); err == nil {
		t.Fatal("real upstream error was hidden")
	}
	var count int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM channels WHERE source_id=1`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("existing channel lost: %d %v", count, err)
	}
	phase = "live"
	if err := a.syncSource(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	var name, id, logo, state string
	if err := a.db.QueryRow(`SELECT name,acestream_id,logo,broadcast_state FROM channels WHERE source_id=1`).Scan(&name, &id, &logo, &state); err != nil {
		t.Fatal(err)
	}
	if name != "Kuwait" || id != "rrn:content:live-videos:new-final" || !strings.Contains(logo, event) || state != "live" {
		t.Fatalf("new live metadata not refreshed: %s %s %s %s", name, id, logo, state)
	}
}

func redBullTestResponse(r *http.Request, body string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}
}

func TestRedBullDiscoverySkipsUnavailableReplayAndUnrelatedErrors(t *testing.T) {
	const newest = "rrn:content:event-profiles:039a06f9-d5fc-445a-ba9a-d6a6bcc5ccf0"
	const older = "rrn:content:event-profiles:039a06f9-d5fc-445a-ba9a-d6a6bcc5ccf1"
	const unrelated = "rrn:content:event-profiles:039a06f9-d5fc-445a-ba9a-d6a6bcc5ccf2"
	a := testApp(t)
	a.sourceClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return redBullTestResponse(r, unrelated+" "+newest+" "+older), nil
	})}
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v3/session":
			return redBullTestResponse(r, `{"token":"test"}`), nil
		case "/v3/products/" + unrelated:
			return nil, errors.New("unrelated event unavailable")
		case "/v3/products/" + newest:
			return redBullTestResponse(r, fmt.Sprintf(`{"id":%q,"title":"OYSHO Premier Padel P1","status":{"code":"post","start_time":"2026-10-18T13:00:00Z","play":"rrn:content:live-videos:newest"}}`, newest)), nil
		case "/v3/products/" + older:
			return redBullTestResponse(r, fmt.Sprintf(`{"id":%q,"title":"Premier   Padel 2026","subheading":"Older playable final","status":{"code":"post","start_time":"2026-10-11T13:00:00Z","play":"rrn:content:live-videos:older"}}`, older)), nil
		case "/v3/products/rrn:content:live-videos:newest":
			return redBullTestResponse(r, `{"playable":false,"status":{"code":"post"}}`), nil
		case "/v3/products/rrn:content:live-videos:older":
			return redBullTestResponse(r, `{"playable":true,"status":{"code":"post"}}`), nil
		default:
			t.Fatalf("unexpected %s", r.URL)
			return nil, nil
		}
	})}
	channels, err := a.fetchRedBullPadelChannels(context.Background(), redBullEventsURL)
	if err != nil || len(channels) != 1 || channels[0].Name != "Older playable final" {
		t.Fatalf("fallback: %#v %v", channels, err)
	}
}

func TestRedBullIdleIsNotAnErrorButBrokenDiscoveryIs(t *testing.T) {
	const event = "rrn:content:event-profiles:039a06f9-d5fc-445a-ba9a-d6a6bcc5ccf0"
	for _, mode := range []string{"upcoming", "broken page", "api error"} {
		t.Run(mode, func(t *testing.T) {
			a := testApp(t)
			if _, err := a.db.Exec(`INSERT INTO sources(id,name,kind,url) VALUES (1,'Padel','redbull_padel',?)`, redBullEventsURL); err != nil {
				t.Fatal(err)
			}
			a.sourceClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				body := event
				if mode == "broken page" {
					body = "unexpected HTML"
				}
				return redBullTestResponse(r, body), nil
			})}
			a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/v3/session" {
					return redBullTestResponse(r, `{"token":"test"}`), nil
				}
				if mode == "api error" {
					return nil, errors.New("upstream down")
				}
				return redBullTestResponse(r, `{"title":"Premier Padel","status":{"code":"pre"}}`), nil
			})}
			err := a.syncSource(context.Background(), 1)
			var status string
			if scanErr := a.db.QueryRow(`SELECT last_status FROM sources WHERE id=1`).Scan(&status); scanErr != nil {
				t.Fatal(scanErr)
			}
			if mode == "upcoming" {
				if err != nil || status != "idle" {
					t.Fatalf("%s %v", status, err)
				}
			} else if err == nil || status != "error" {
				t.Fatalf("failure hidden: %s %v", status, err)
			}
		})
	}
}

func TestRedBullRotationPreservesExportURLAndUserSettings(t *testing.T) {
	a := testApp(t)
	if _, err := a.db.Exec(`INSERT INTO sources(id,name,kind) VALUES(1,'Padel','redbull_padel')`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, _, _, err := a.replaceChannels(ctx, 1, []playlist.Channel{{Name: "Milan", AceStreamID: "rrn:content:live-videos:old", BroadcastState: "replay"}}); err != nil {
		t.Fatal(err)
	}
	var beforeID int64
	var beforeTVG string
	if err := a.db.QueryRow(`SELECT id,tvg_id FROM channels WHERE source_id=1`).Scan(&beforeID, &beforeTVG); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`UPDATE channels SET enabled=0,metadata_locked=1,name='My Padel' WHERE id=?`, beforeID); err != nil {
		t.Fatal(err)
	}
	added, updated, removed, err := a.replaceChannels(ctx, 1, []playlist.Channel{{Name: "Kuwait", AceStreamID: "rrn:content:live-videos:new", BroadcastState: "live"}})
	if err != nil || added != 0 || updated != 1 || removed != 0 {
		t.Fatalf("rotation: %d %d %d %v", added, updated, removed, err)
	}
	var afterID int64
	var name, video, state, tvg string
	var enabled int
	if err := a.db.QueryRow(`SELECT id,name,acestream_id,broadcast_state,enabled,tvg_id FROM channels WHERE source_id=1`).Scan(&afterID, &name, &video, &state, &enabled, &tvg); err != nil {
		t.Fatal(err)
	}
	if afterID != beforeID || name != "My Padel" || video != "rrn:content:live-videos:new" || state != "live" || enabled != 0 || tvg != beforeTVG {
		t.Fatalf("settings lost: %d %s %s %s %d %s", afterID, name, video, state, enabled, tvg)
	}
}

func TestRedBullDiscoveryPrioritizesLiveOverNewestReplay(t *testing.T) {
	const replay = "rrn:content:event-profiles:039a06f9-d5fc-445a-ba9a-d6a6bcc5ccf0"
	const live = "rrn:content:event-profiles:039a06f9-d5fc-445a-ba9a-d6a6bcc5ccf1"
	for _, hasLive := range []bool{false, true} {
		t.Run(map[bool]string{false: "latest replay", true: "live wins"}[hasLive], func(t *testing.T) {
			a := testApp(t)
			defer a.Close()
			a.sourceClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) { return redBullTestResponse(r, replay+" "+live), nil })}
			a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Path, "live-videos:") {
					return redBullTestResponse(r, `{"playable":true,"status":{"code":"post"}}`), nil
				}
				id, code, date := replay, "post", "2026-10-18T13:00:00Z"
				if strings.HasSuffix(r.URL.Path, live) {
					id, date = live, "2026-10-11T13:00:00Z"
					if hasLive {
						code = "live"
					}
				}
				return redBullTestResponse(r, `{"id":"`+id+`","title":"Premier Padel","status":{"code":"`+code+`","start_time":"`+date+`","play":"rrn:content:live-videos:test"}}`), nil
			})}
			p, _, err := a.discoverRedBullPadelEvent(context.Background(), redBullEventsURL, "test")
			want := replay
			if hasLive {
				want = live
			}
			if err != nil || p.ID != want {
				t.Fatalf("got %s %v, want %s", p.ID, err, want)
			}
		})
	}
}

func TestRedBullUnavailableReplayIsNotReportedHealthy(t *testing.T) {
	a := testApp(t)
	defer a.Close()
	const event = "rrn:content:event-profiles:039a06f9-d5fc-445a-ba9a-d6a6bcc5ccf0"
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"playable":false,"status":{"code":"post"}}`
		if r.URL.Path == "/v3/session" {
			body = `{"token":"test"}`
		} else if strings.HasSuffix(r.URL.Path, event) {
			body = `{"id":"` + event + `","status":{"code":"post","play":"rrn:content:live-videos:test"}}`
		}
		return redBullTestResponse(r, body), nil
	})}
	if _, err := a.fetchRedBullPadelChannels(context.Background(), "https://www.redbull.tv/"+event); err == nil {
		t.Fatal("unavailable replay accepted")
	}
}

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
