package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/anubisreal/acebridge/internal/playlist"
)

const (
	redBullEventsURL = "https://www.redbull.tv/es_ES/events"
	redBullAPIBase   = "https://api.redbull.tv/v3"
)

var redBullEventIDPattern = regexp.MustCompile(`rrn:content:event-profiles:[0-9a-f-]{36}`)

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
	req.Header.Set("User-Agent", "AceBridge/0.3")
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
	req.Header.Set("User-Agent", "AceBridge/0.3")
	return a.fetchRedBullJSON(req, target)
}

func (a *App) discoverRedBullPadelEvent(ctx context.Context, sourceURL, token string) (redBullProduct, string, error) {
	if match := redBullEventIDPattern.FindString(sourceURL); match != "" {
		var product redBullProduct
		if err := a.redBullAPIGet(ctx, token, "/products/"+url.PathEscape(match), &product); err != nil {
			return product, "", err
		}
		for _, collection := range product.Collections {
			if strings.HasSuffix(collection.ID, ":live_programs") {
				return product, collection.ID, nil
			}
		}
		return product, "", errors.New("the Red Bull event has no live schedule")
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	req.Header.Set("User-Agent", "AceBridge/0.3")
	response, err := a.sourceClient.Do(req)
	if err != nil {
		return redBullProduct{}, "", fmt.Errorf("cannot discover Premier Padel event: %w", err)
	}
	body, err := readLimitedBody(response, 8<<20)
	if err != nil {
		return redBullProduct{}, "", err
	}
	page := string(body)
	seen := map[string]bool{}
	var fallbackProduct redBullProduct
	var fallbackCollection string
	for _, location := range redBullEventIDPattern.FindAllStringIndex(page, -1) {
		id := page[location[0]:location[1]]
		if seen[id] {
			continue
		}
		seen[id] = true
		var product redBullProduct
		if a.redBullAPIGet(ctx, token, "/products/"+url.PathEscape(id), &product) != nil || !strings.EqualFold(product.Title, "Premier Padel") {
			continue
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
	if fallbackCollection != "" {
		return fallbackProduct, fallbackCollection, nil
	}
	return redBullProduct{}, "", errors.New("no current Premier Padel event was found")
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
	var collection redBullCollection
	if err := a.redBullAPIGet(ctx, token, "/collections/"+url.PathEscape(collectionID), &collection); err != nil {
		return nil, err
	}
	group := strings.TrimSpace(event.Title)
	if group == "" {
		group = "Premier Padel"
	}
	channels := make([]playlist.Channel, 0, len(collection.Items))
	for _, item := range collection.Items {
		if item.Status.Code != "live" || !item.Playable || item.ID == "" {
			continue
		}
		name := strings.TrimSpace(item.Title)
		if name == "" {
			name = "Premier Padel en directo"
		}
		logo := "https://resources.redbull.tv/" + item.ID + "/rbtv_display_art_square/f_webp,c_fill,w_512,h_512,q_80?namespace=rbtv&refresh=true"
		channels = append(channels, playlist.Channel{TVGID: item.ID, Name: name, Group: group, Logo: logo, AceStreamID: item.ID})
	}
	return channels, nil
}

func (a *App) redBullPlaybackURL(ctx context.Context, rrn string) (string, error) {
	if !strings.HasPrefix(rrn, "rrn:content:live-videos:") {
		return "", errors.New("invalid Red Bull TV stream identifier")
	}
	token, err := a.redBullToken(ctx)
	if err != nil {
		return "", err
	}
	return "https://dms.redbull.tv/v3/" + url.PathEscape(rrn) + "/" + url.PathEscape(token) + "/playlist.m3u8", nil
}

func allowedRedBullStreamURL(target *url.URL) bool {
	if target == nil || target.Scheme != "https" {
		return false
	}
	host := strings.ToLower(target.Hostname())
	return host == "dms.redbull.tv" || host == "rbmn-live.akamaized.net"
}
