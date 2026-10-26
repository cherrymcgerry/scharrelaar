// Package websearch looks things up on the web through DuckDuckGo's plain HTML
// results page, which needs no account. It is used for the brand and model an
// ad names, so a small local model can read what is known about them instead of
// guessing.
package websearch

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Result is one search result.
type Result struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// ErrBlocked is returned when the search engine refuses automated requests.
var ErrBlocked = errors.New("the search engine refused the request")

// Client searches DuckDuckGo. The zero value is ready to use.
type Client struct {
	HTTP *http.Client
	// BaseURL overrides https://html.duckduckgo.com, for tests.
	BaseURL string
	// Gap is the least time between two searches; 0 means 5 seconds.
	Gap time.Duration

	mu   sync.Mutex
	last time.Time
}

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36"

var (
	blockRE   = regexp.MustCompile(`<div class="result results_links[^"]*"`)
	titleRE   = regexp.MustCompile(`(?s)class="result__a" href="([^"]*)"[^>]*>(.*?)</a>`)
	snippetRE = regexp.MustCompile(`(?s)class="result__snippet"[^>]*>(.*?)</a>`)
	tagRE     = regexp.MustCompile(`<[^>]+>`)
)

// Search returns up to max results for query.
func (c *Client) Search(ctx context.Context, query string, max int) ([]Result, error) {
	if err := c.wait(ctx); err != nil {
		return nil, err
	}
	base := c.BaseURL
	if base == "" {
		base = "https://html.duckduckgo.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/html/?q="+url.QueryEscape(query), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	page := string(body)
	switch {
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusAccepted:
		return nil, ErrBlocked
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("search: HTTP %d", resp.StatusCode)
	case strings.Contains(page, "anomaly-modal") || strings.Contains(page, "challenge-form"):
		return nil, ErrBlocked
	}
	return parse(page, max), nil
}

// wait keeps searches at least Gap apart.
func (c *Client) wait(ctx context.Context) error {
	gap := c.Gap
	if gap == 0 {
		gap = 5 * time.Second
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if d := time.Until(c.last.Add(gap)); d > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
	}
	c.last = time.Now()
	return nil
}

func parse(page string, max int) []Result {
	starts := blockRE.FindAllStringIndex(page, -1)
	var out []Result
	for i, s := range starts {
		if len(out) == max {
			break
		}
		end := len(page)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		block := page[s[0]:end]
		if strings.Contains(page[s[0]:s[1]], "result--ad") {
			continue
		}
		title := titleRE.FindStringSubmatch(block)
		snippet := snippetRE.FindStringSubmatch(block)
		if title == nil || snippet == nil {
			continue
		}
		out = append(out, Result{Title: text(title[2]), URL: target(title[1]), Snippet: text(snippet[1])})
	}
	return out
}

func text(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagRE.ReplaceAllString(s, ""))), " ")
}

// target unwraps DuckDuckGo's redirect link to the page it points to.
func target(href string) string {
	href = html.UnescapeString(href)
	if u, err := url.Parse(href); err == nil {
		if t := u.Query().Get("uddg"); t != "" {
			return t
		}
	}
	return href
}
