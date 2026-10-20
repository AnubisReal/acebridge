package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/anubisreal/acebridge/internal/playlist"
)

const (
	redBullEventsURL       = "https://www.redbull.tv/es_ES/events"
	redBullPadelURL        = "https://www.redbull.tv/es_ES/partner-channel/rrn:content:tv-partner-channels:7bbfd0ef-86fd-40fd-be46-f245c351b77a"
	redBullAPIBase         = "https://api.redbull.tv/v3"
	redBullPlaybackBaseURL = "https://play.redbull.com/main/v1/rbtv/es_ES/es/personal_computer/http"
	redBullUserAgent       = "AceBridge/0.3"
)

var redBullEventIDPattern = regexp.MustCompile(`rrn:content:event-profiles:[0-9a-f-]{36}`)

var errNoRedBullEvent = errors.New("no playable Premier Padel event was found")

type redBullSession struct {
	Token string `json:"token"`
}

type redBullProduct struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Subheading  string `json:"subheading"`
	ContentType string `json:"content_type"`
	Playable    bool   `json:"playable"`
	Status      struct {
		Code      string `json:"code"`
		Play      string `json:"play"`
		StartTime string `json:"start_time"`
		EndTime   string `json:"end_time"`
	} `json:"status"`
	Collections []struct {
		ID string `json:"id"`
	} `json:"collections"`
}

type redBullCollection struct {
	Items []redBullProduct `json:"items"`
}

func (a *App) redBullToken(ctx context.Context) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, redBullAPIBase+"/session?category=personal_computer&os_family=http&locale=es", nil)
	req.Header.Set("User-Agent", redBullUserAgent)
	var session redBullSession
	if err := a.fetchRedBullJSON(req, &session); err != nil {
		return "", err
	}
	if session.Token == "" {
		return "", errors.New("invalid Red Bull TV session")
	}
	return session.Token, nil
}

func (a *App) fetchRedBullJSON(req *http.Request, target any) error {
	response, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("request to Red Bull TV failed: %w", err)
	}
	data, err := readLimitedBody(response, 5<<20)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return errors.New("invalid data returned by Red Bull TV")
	}
	return nil
}

func (a *App) redBullAPIGet(ctx context.Context, token, endpoint string, target any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, redBullAPIBase+endpoint, nil)
	req.Header.Set("Authorization", token)
	req.Header.Set("Accept-Language", "es-ES,es;q=0.9")
	req.Header.Set("User-Agent", redBullUserAgent)
	return a.fetchRedBullJSON(req, target)
}

func (a *App) discoverRedBullPadelEvent(ctx context.Context, sourceURL, token string) (redBullProduct, string, error) {
	if match := redBullEventIDPattern.FindString(sourceURL); match != "" {
		var product redBullProduct
		if err := a.redBullAPIGet(ctx, token, "/products/"+url.PathEscape(match), &product); err != nil {
			return product, "", err
		}
		if redBullLiveVideoID(product) != "" || redBullReplayVideoID(product) != "" {
			return product, "", nil
		}
		for _, collection := range product.Collections {
			if strings.HasSuffix(collection.ID, ":live_programs") {
				return product, collection.ID, nil
			}
		}
		return product, "", errNoRedBullEvent
	}
	page, err := a.redBullCatalogPage(ctx, sourceURL)
	if err != nil {
		return redBullProduct{}, "", err
	}
	seen := map[string]bool{}
	var fallbackProduct redBullProduct
	var fallbackCollection string
	var replays []redBullProduct
	var lookupErrors []error
	if !redBullEventIDPattern.MatchString(page) {
		return redBullProduct{}, "", errors.New("catalog returned by Red Bull contains no recognizable events")
	}
	for _, location := range redBullEventIDPattern.FindAllStringIndex(page, -1) {
		id := page[location[0]:location[1]]
		if seen[id] {
			continue
		}
		seen[id] = true
		var product redBullProduct
		if err := a.redBullAPIGet(ctx, token, "/products/"+url.PathEscape(id), &product); err != nil {
			lookupErrors = append(lookupErrors, err)
			continue
		}
		if !strings.Contains(strings.ToLower(strings.Join(strings.Fields(product.Title), " ")), "premier padel") {
			continue
		}
		if redBullLiveVideoID(product) != "" {
			return product, "", nil
		}
		if redBullReplayVideoID(product) != "" {
			replays = append(replays, product)
		}
		for _, collection := range product.Collections {
			if strings.HasSuffix(collection.ID, ":live_programs") {
				if product.Status.Code == "live" {
					return product, collection.ID, nil
				}
				if fallbackCollection == "" {
					fallbackProduct, fallbackCollection = product, collection.ID
				}
			}
		}
	}
	sort.SliceStable(replays, func(i, j int) bool { return redBullEventTime(replays[i]).After(redBullEventTime(replays[j])) })
	for _, replay := range replays {
		available, err := a.redBullReplayAvailable(ctx, token, redBullReplayVideoID(replay))
		if err != nil {
			lookupErrors = append(lookupErrors, err)
			continue
		}
		if available {
			return replay, "", nil
		}
	}
	if fallbackCollection != "" {
		return fallbackProduct, fallbackCollection, nil
	}
	if len(lookupErrors) != 0 {
		return redBullProduct{}, "", fmt.Errorf("catalog returned by Red Bull could not be fully checked: %w", errors.Join(lookupErrors...))
	}
	return redBullProduct{}, "", errNoRedBullEvent
}

func (a *App) redBullCatalogPage(ctx context.Context, sourceURL string) (string, error) {
	targets := []string{sourceURL}
	if sourceURL == redBullEventsURL {
		targets = []string{redBullPadelURL, redBullEventsURL}
	}
	var failures []error
	for _, target := range targets {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("User-Agent", redBullUserAgent)
		response, err := a.sourceClient.Do(req)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		body, err := readLimitedBody(response, 8<<20)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if !redBullEventIDPattern.Match(body) {
			failures = append(failures, errors.New("catalog returned by Red Bull contains no recognizable events"))
			continue
		}
		return string(body), nil
	}
	return "", fmt.Errorf("cannot discover Premier Padel catalog: %w", errors.Join(failures...))
}

func redBullEventTime(product redBullProduct) time.Time {
	stamp, _ := time.Parse(time.RFC3339, product.Status.StartTime)
	return stamp
}

func redBullReplayVideoID(product redBullProduct) string {
	if product.Status.Code == "post" && strings.HasPrefix(product.Status.Play, "rrn:content:live-videos:") {
		return product.Status.Play
	}
	return ""
}

// Event profiles can finish while their video remains playable as a replay.
// Validate the video product rather than assuming that a finished event is broken.
func (a *App) redBullReplayAvailable(ctx context.Context, token, id string) (bool, error) {
	var video redBullProduct
	if err := a.redBullAPIGet(ctx, token, "/products/"+url.PathEscape(id), &video); err != nil {
		return false, err
	}
	return video.Playable && video.Status.Code == "post", nil
}

// A catalog entry may disappear before its video expires. Only use this fallback
// after a successful discovery with no candidates, never to hide network errors.
func (a *App) retainedRedBullChannels(ctx context.Context, sourceID int64) ([]playlist.Channel, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT tvg_id,name,group_name,logo,source_acestream_id FROM channels WHERE source_id=?`, sourceID)
	if err != nil {
		return nil, err
	}
	var saved []playlist.Channel
	for rows.Next() {
		var channel playlist.Channel
		if err := rows.Scan(&channel.TVGID, &channel.Name, &channel.Group, &channel.Logo, &channel.AceStreamID); err != nil {
			rows.Close()
			return nil, err
		}
		saved = append(saved, channel)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(saved) == 0 {
		return nil, errNoRedBullEvent
	}
	token, err := a.redBullToken(ctx)
	if err != nil {
		return nil, err
	}
	var channels []playlist.Channel
	for _, channel := range saved {
		if !strings.HasPrefix(channel.AceStreamID, "rrn:content:live-videos:") {
			continue
		}
		var video redBullProduct
		if err := a.redBullAPIGet(ctx, token, "/products/"+url.PathEscape(channel.AceStreamID), &video); err != nil {
			return nil, err
		}
		if !video.Playable || (video.Status.Code != "live" && video.Status.Code != "post") {
			continue
		}
		channel.BroadcastState = "live"
		if video.Status.Code == "post" {
			channel.BroadcastState = "replay"
		}
		channels = append(channels, channel)
	}
	if len(channels) == 0 {
		return nil, errNoRedBullEvent
	}
	return channels, nil
}

func redBullLiveVideoID(product redBullProduct) string {
	if product.Status.Code != "live" || !strings.HasPrefix(product.Status.Play, "rrn:content:live-videos:") {
		return ""
	}
	return product.Status.Play
}

func (a *App) fetchRedBullPadelChannels(ctx context.Context, sourceURL string) ([]playlist.Channel, error) {
	token, err := a.redBullToken(ctx)
	if err != nil {
		return nil, err
	}
	event, collectionID, err := a.discoverRedBullPadelEvent(ctx, sourceURL, token)
	if err != nil {
		return nil, err
	}
	group := strings.TrimSpace(event.Title)
	if group == "" {
		group = "Premier Padel"
	}
	videoID, broadcastState := redBullLiveVideoID(event), "live"
	if videoID == "" {
		videoID, broadcastState = redBullReplayVideoID(event), "replay"
		if videoID != "" {
			available, err := a.redBullReplayAvailable(ctx, token, videoID)
			if err != nil {
				return nil, err
			}
			if !available {
				return nil, errNoRedBullEvent
			}
		}
	}
	if liveVideoID := videoID; liveVideoID != "" {
		name := strings.TrimSpace(event.Subheading)
		if name == "" {
			name = group
		}
		logoID := event.ID
		if logoID == "" {
			logoID = liveVideoID
		}
		logo := "https://resources.redbull.tv/" + logoID + "/rbtv_display_art_square/f_webp,c_fill,w_512,h_512,q_80?namespace=rbtv&refresh=true"
		return []playlist.Channel{{TVGID: liveVideoID, Name: name, Group: group, Logo: logo, AceStreamID: liveVideoID, BroadcastState: broadcastState}}, nil
	}
	var collection redBullCollection
	if err := a.redBullAPIGet(ctx, token, "/collections/"+url.PathEscape(collectionID), &collection); err != nil {
		return nil, err
	}
	channels := make([]playlist.Channel, 0, len(collection.Items))
	for _, item := range collection.Items {
		if (item.Status.Code != "live" && item.Status.Code != "post") || !item.Playable || !strings.HasPrefix(item.ID, "rrn:content:live-videos:") {
			continue
		}
		name := strings.TrimSpace(item.Title)
		if name == "" {
			name = "Premier Padel"
		}
		logo := "https://resources.redbull.tv/" + item.ID + "/rbtv_display_art_square/f_webp,c_fill,w_512,h_512,q_80?namespace=rbtv&refresh=true"
		state := "live"
		if item.Status.Code == "post" {
			state = "replay"
		}
		channels = append(channels, playlist.Channel{TVGID: item.ID, Name: name, Group: group, Logo: logo, AceStreamID: item.ID, BroadcastState: state})
	}
	if len(channels) == 0 {
		return nil, errNoRedBullEvent
	}
	return channels, nil
}

func (a *App) redBullPlaybackURL(ctx context.Context, rrn string) (string, error) {
	if !strings.HasPrefix(rrn, "rrn:content:live-videos:") {
		return "", errors.New("invalid Red Bull TV stream identifier")
	}
	return redBullPlaybackBaseURL + "/" + url.PathEscape(rrn) + ".m3u8?device_group=group_5", nil
}

func allowedRedBullStreamURL(target *url.URL) bool {
	if target == nil || target.Scheme != "https" {
		return false
	}
	host := strings.ToLower(target.Hostname())
	return host == "play.redbull.com" || host == "rbmn-live.akamaized.net"
}
