package sources

import (
	"html"
	"net/url"
	"regexp"
	"strings"
)

var (
	htmlLinkTagPattern = regexp.MustCompile(`(?is)<link\b[^>]*>`)
	htmlAttrPattern    = regexp.MustCompile("(?is)([a-zA-Z_:][-a-zA-Z0-9_:.]*)\\s*=\\s*(\"([^\"]*)\"|'([^']*)'|([^\\s\"'=<>`]+))")
)

func discoverPodcastRSSURL(body []byte, base *url.URL) (string, bool) {
	if base == nil {
		return "", false
	}
	for _, rawTag := range htmlLinkTagPattern.FindAll(body, -1) {
		attrs := parseHTMLAttrs(string(rawTag))
		if !linkAdvertisesPodcastRSS(attrs) {
			continue
		}
		href := strings.TrimSpace(attrs["href"])
		if href == "" {
			continue
		}
		discovered, err := base.Parse(href)
		if err != nil || discovered == nil || discovered.Host == "" {
			continue
		}
		switch strings.ToLower(discovered.Scheme) {
		case "http", "https":
		default:
			continue
		}
		discovered.Fragment = ""
		return discovered.String(), true
	}
	return "", false
}

func parseHTMLAttrs(tag string) map[string]string {
	attrs := map[string]string{}
	for _, match := range htmlAttrPattern.FindAllStringSubmatch(tag, -1) {
		if len(match) < 6 {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(match[1]))
		value := firstNonEmpty(match[3], match[4], match[5])
		if name != "" {
			attrs[name] = html.UnescapeString(value)
		}
	}
	return attrs
}

func linkAdvertisesPodcastRSS(attrs map[string]string) bool {
	href := strings.TrimSpace(attrs["href"])
	if href == "" {
		return false
	}
	rel := strings.ToLower(strings.TrimSpace(attrs["rel"]))
	contentType := strings.ToLower(strings.TrimSpace(attrs["type"]))
	if !strings.Contains(contentType, "rss") && !strings.Contains(contentType, "atom") && !strings.Contains(contentType, "xml") {
		return false
	}
	if rel == "" {
		return true
	}
	for _, token := range strings.Fields(rel) {
		if token == "alternate" || token == "self" {
			return true
		}
	}
	return rel == "alternate" || rel == "self"
}
