package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/anubisreal/acebridge/internal/playlist"
)

const logoMatchThreshold = 0.88

type iptvOrgChannel struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	AltNames []string `json:"alt_names"`
}

type iptvOrgLogo struct {
	Channel string   `json:"channel"`
	Feed    *string  `json:"feed"`
	InUse   bool     `json:"in_use"`
	Tags    []string `json:"tags"`
	Width   int      `json:"width"`
	Height  int      `json:"height"`
	Format  string   `json:"format"`
	URL     string   `json:"url"`
}

type catalogName struct {
	channelID string
	value     string
}

type logoCatalog struct {
	byID      map[string]string
	exactName map[string][]string
	names     []catalogName
}

type logoCatalogCache struct {
	mu       sync.Mutex
	value    *logoCatalog
	loadedAt time.Time
}

func (a *App) enrichChannelLogos(ctx context.Context, channels []playlist.Channel, replaceExisting bool) {
	if strings.TrimSpace(a.cfg.IPTVOrgAPIBase) == "" {
		return
	}
	catalog, err := a.loadLogoCatalog(ctx)
	if err != nil {
		a.logger.Warn("IPTV-org logo catalog unavailable", "error", err)
		return
	}
	for i := range channels {
		if channels[i].Logo != "" && !replaceExisting {
			continue
		}
		if logo := catalog.match(channels[i].TVGID, channels[i].Name); logo != "" {
			channels[i].Logo = logo
		}
	}
}

func (a *App) matchChannelLogo(ctx context.Context, tvgID, name string) string {
	if strings.TrimSpace(a.cfg.IPTVOrgAPIBase) == "" {
		return ""
	}
	catalog, err := a.loadLogoCatalog(ctx)
	if err != nil {
		a.logger.Warn("IPTV-org logo catalog unavailable", "error", err)
		return ""
	}
	return catalog.match(tvgID, name)
}

func (a *App) loadLogoCatalog(ctx context.Context) (*logoCatalog, error) {
	a.logoCatalog.mu.Lock()
	defer a.logoCatalog.mu.Unlock()
	if a.logoCatalog.value != nil && time.Since(a.logoCatalog.loadedAt) < 24*time.Hour {
		return a.logoCatalog.value, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	base := strings.TrimRight(a.cfg.IPTVOrgAPIBase, "/")
	var channels []iptvOrgChannel
	if err := a.fetchCatalogJSON(ctx, base+"/channels.json", &channels); err != nil {
		return nil, err
	}
	var logos []iptvOrgLogo
	if err := a.fetchCatalogJSON(ctx, base+"/logos.json", &logos); err != nil {
		return nil, err
	}
	catalog := buildLogoCatalog(channels, logos)
	a.logoCatalog.value = catalog
	a.logoCatalog.loadedAt = time.Now()
	return catalog, nil
}

func (a *App) fetchCatalogJSON(ctx context.Context, address string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "AceBridge/0.1")
	response, err := a.client.Do(request)
	if err != nil {
		return err
	}
	data, err := readLimitedBody(response, 25<<20)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func buildLogoCatalog(channels []iptvOrgChannel, logos []iptvOrgLogo) *logoCatalog {
	byChannel := make(map[string][]iptvOrgLogo)
	for _, logo := range logos {
		if validRemoteLogoURL(logo.URL) {
			key := strings.ToLower(strings.TrimSpace(logo.Channel))
			byChannel[key] = append(byChannel[key], logo)
		}
	}
	catalog := &logoCatalog{byID: make(map[string]string), exactName: make(map[string][]string)}
	for _, channel := range channels {
		id := strings.ToLower(strings.TrimSpace(channel.ID))
		logo := chooseLogo(byChannel[id])
		if id == "" || logo == "" {
			continue
		}
		catalog.byID[id] = logo
		for _, name := range append([]string{channel.Name}, channel.AltNames...) {
			normalized := normalizeChannelName(name)
			if normalized == "" {
				continue
			}
			catalog.exactName[normalized] = appendUnique(catalog.exactName[normalized], id)
			catalog.names = append(catalog.names, catalogName{channelID: id, value: normalized})
		}
	}
	return catalog
}

func (catalog *logoCatalog) match(tvgID, name string) string {
	if id := strings.ToLower(strings.TrimSpace(tvgID)); id != "" {
		if logo := catalog.byID[id]; logo != "" {
			return logo
		}
	}
	normalized := normalizeChannelName(name)
	if normalized == "" {
		return ""
	}
	if ids := catalog.exactName[normalized]; len(ids) == 1 {
		return catalog.byID[ids[0]]
	}
	if utf8.RuneCountInString(normalized) < 4 {
		return ""
	}
	bestID := ""
	best, second := 0.0, 0.0
	wantedLength := utf8.RuneCountInString(normalized)
	first, _ := utf8.DecodeRuneInString(normalized)
	for _, candidate := range catalog.names {
		candidateFirst, _ := utf8.DecodeRuneInString(candidate.value)
		candidateLength := utf8.RuneCountInString(candidate.value)
		if candidateFirst != first || abs(candidateLength-wantedLength) > max(3, wantedLength/4) {
			continue
		}
		score := nameSimilarity(normalized, candidate.value)
		if candidate.channelID == bestID {
			if score > best {
				best = score
			}
			continue
		}
		if score > best {
			second = best
			best, bestID = score, candidate.channelID
		} else if score > second {
			second = score
		}
	}
	if best >= logoMatchThreshold && best-second >= 0.04 {
		return catalog.byID[bestID]
	}
	return ""
}

func chooseLogo(items []iptvOrgLogo) string {
	type ranked struct {
		logo  iptvOrgLogo
		score int
	}
	choices := make([]ranked, 0, len(items))
	for _, logo := range items {
		score := min(logo.Width, 2000) / 100
		if logo.InUse {
			score += 100
		}
		if logo.Feed == nil {
			score += 20
		}
		if strings.EqualFold(logo.Format, "SVG") {
			score += 15
		}
		if hasTag(logo.Tags, "horizontal") {
			score += 5
		}
		if hasTag(logo.Tags, "white") {
			score -= 60
		}
		choices = append(choices, ranked{logo: logo, score: score})
	}
	sort.SliceStable(choices, func(i, j int) bool { return choices[i].score > choices[j].score })
	if len(choices) == 0 {
		return ""
	}
	return choices[0].logo.URL
}

func normalizeChannelName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.NewReplacer(
		"á", "a", "à", "a", "ä", "a", "â", "a", "ã", "a",
		"é", "e", "è", "e", "ë", "e", "ê", "e",
		"í", "i", "ì", "i", "ï", "i", "î", "i",
		"ó", "o", "ò", "o", "ö", "o", "ô", "o", "õ", "o",
		"ú", "u", "ù", "u", "ü", "u", "û", "u", "ñ", "n", "ç", "c",
	).Replace(value)
	words := strings.FieldsFunc(value, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	filtered := words[:0]
	for _, word := range words {
		switch word {
		case "hd", "fhd", "uhd", "4k", "1080p", "720p", "tv", "channel", "canal":
			continue
		default:
			filtered = append(filtered, word)
		}
	}
	return strings.Join(filtered, "")
}

func nameSimilarity(left, right string) float64 {
	a, b := []rune(left), []rune(right)
	longest := max(len(a), len(b))
	if longest == 0 {
		return 1
	}
	return 1 - float64(levenshtein(a, b))/float64(longest)
}

func levenshtein(left, right []rune) int {
	previous := make([]int, len(right)+1)
	for j := range previous {
		previous[j] = j
	}
	for i, l := range left {
		current := make([]int, len(right)+1)
		current[0] = i + 1
		for j, r := range right {
			cost := 0
			if l != r {
				cost = 1
			}
			current[j+1] = min(current[j]+1, previous[j+1]+1, previous[j]+cost)
		}
		previous = current
	}
	return previous[len(right)]
}

func hasTag(tags []string, wanted string) bool {
	for _, tag := range tags {
		if strings.EqualFold(tag, wanted) {
			return true
		}
	}
	return false
}

func validRemoteLogoURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Hostname() != "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func appendUnique(items []string, value string) []string {
	for _, item := range items {
		if item == value {
			return items
		}
	}
	return append(items, value)
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
