package metadata

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

func getJSON[T any](client *http.Client, req *http.Request) (T, error) {
	var out T
	resp, err := client.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, fmt.Errorf("status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, err
	}
	return out, nil
}

func withQuery(baseURL string, values url.Values) string {
	if strings.Contains(baseURL, "?") {
		return baseURL + "&" + values.Encode()
	}
	return baseURL + "?" + values.Encode()
}

func contributors(names []string, role string) []catalog.ContributorRef {
	items := make([]catalog.ContributorRef, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			items = append(items, catalog.ContributorRef{Name: name, Role: role})
		}
	}
	return items
}

func first(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func unique(values []string) []string {
	seen := map[string]struct{}{}
	uniqueValues := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		uniqueValues = append(uniqueValues, value)
	}
	return uniqueValues
}

func yearFromDate(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 4 {
		return value[:4]
	}
	return value
}

func atoi(value string) int {
	parsed, _ := strconv.Atoi(strings.TrimSpace(value))
	return parsed
}

func scoreFromString(value string) int {
	score := atoi(value)
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

func msToSeconds(ms int) int {
	if ms <= 0 {
		return 0
	}
	return ms / 1000
}
