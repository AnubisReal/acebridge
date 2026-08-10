package playlist

import (
	"bufio"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
)

type Channel struct {
	TVGID       string `json:"tvg_id"`
	Name        string `json:"name"`
	Group       string `json:"group"`
	Logo        string `json:"logo"`
	AceStreamID string `json:"acestream_id"`
}

var (
	attributePattern = regexp.MustCompile(`([\w-]+)=(?:"([^"]*)"|'([^']*)')`)
	aceIDPattern     = regexp.MustCompile(`(?i)^[a-f0-9]{40}$`)
)

func Parse(content string) []Channel {
	if channels := parseJSON(content); len(channels) > 0 {
		return channels
	}
	if channels := parseM3U(content); len(channels) > 0 {
		return channels
	}
	return parsePlainText(content)
}

// parsePlainText supports simple lists made of alternating channel-name and
// AceStream-ID lines. Empty lines and comments are ignored.
func parsePlainText(content string) []Channel {
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	channels := make([]Channel, 0)
	pendingName := ""
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if id := ExtractAceStreamID(line); id != "" {
			name := strings.TrimSpace(pendingName)
			if name == "" {
				name = "AceStream " + id[:8]
			}
			channels = append(channels, Channel{Name: name, AceStreamID: id})
			pendingName = ""
			continue
		}
		pendingName = line
	}
	return channels
}

func parseM3U(content string) []Channel {
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	var pending *Channel
	channels := make([]Channel, 0)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		normalized := strings.TrimSpace(strings.TrimPrefix(line, "#"))
		if strings.HasPrefix(strings.ToUpper(normalized), "EXTINF:") {
			channel := parseMetadata(line)
			pending = &channel
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") || pending == nil {
			continue
		}

		if id := ExtractAceStreamID(line); id != "" {
			pending.AceStreamID = id
			channels = append(channels, *pending)
		}
		pending = nil
	}
	return channels
}

func parseJSON(content string) []Channel {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') {
		return nil
	}

	type item struct {
		Title       string `json:"title"`
		Name        string `json:"name"`
		Hash        string `json:"hash"`
		AceStreamID string `json:"acestream_id"`
		Group       string `json:"group"`
		Logo        string `json:"logo"`
		TVGID       string `json:"tvg_id"`
	}
	type document struct {
		Hashes   []item `json:"hashes"`
		Channels []item `json:"channels"`
	}

	var items []item
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal([]byte(trimmed), &items); err != nil {
			return nil
		}
	} else {
		var payload document
		if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
			return nil
		}
		items = payload.Hashes
		if len(items) == 0 {
			items = payload.Channels
		}
	}

	channels := make([]Channel, 0, len(items))
	for _, entry := range items {
		id := ExtractAceStreamID(entry.Hash)
		if id == "" {
			id = ExtractAceStreamID(entry.AceStreamID)
		}
		if id == "" {
			continue
		}
		name := strings.TrimSpace(entry.Title)
		if name == "" {
			name = strings.TrimSpace(entry.Name)
		}
		if name == "" {
			name = strings.TrimSpace(entry.TVGID)
		}
		if name == "" {
			name = "AceStream " + id[:8]
		}
		channels = append(channels, Channel{
			TVGID:       strings.TrimSpace(entry.TVGID),
			Name:        name,
			Group:       strings.TrimSpace(entry.Group),
			Logo:        strings.TrimSpace(entry.Logo),
			AceStreamID: id,
		})
	}
	return channels
}

func parseMetadata(line string) Channel {
	attrs := make(map[string]string)
	for _, match := range attributePattern.FindAllStringSubmatch(line, -1) {
		value := match[2]
		if value == "" {
			value = match[3]
		}
		attrs[strings.ToLower(match[1])] = strings.TrimSpace(value)
	}

	name := ""
	if comma := strings.LastIndex(line, ","); comma >= 0 {
		name = strings.TrimSpace(line[comma+1:])
	}
	if attrs["tvg-name"] != "" {
		name = attrs["tvg-name"]
	}
	if name == "" {
		name = attrs["tvg-id"]
	}

	return Channel{TVGID: attrs["tvg-id"], Name: name, Group: attrs["group-title"], Logo: attrs["tvg-logo"]}
}

func ExtractAceStreamID(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if aceIDPattern.MatchString(strings.ToLower(trimmed)) {
		return strings.ToLower(trimmed)
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "acestream://") {
		id := strings.TrimPrefix(strings.ToLower(trimmed), "acestream://")
		if aceIDPattern.MatchString(id) {
			return id
		}
	}
	parsed, err := url.Parse(trimmed)
	if err == nil {
		id := strings.ToLower(parsed.Query().Get("id"))
		if aceIDPattern.MatchString(id) {
			return id
		}
	}
	return ""
}
