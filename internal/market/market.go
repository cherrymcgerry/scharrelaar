// Package market talks to Adevinta classifieds sites (2dehands.be, marktplaats.nl).
// Both expose the same undocumented search API and the same detail page layout, so one
// client serves every site; only the domain and the category names differ.
package market

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	configMarker = "window.__CONFIG__ = "
	userAgent    = "scharrelaar/0.1 (personal listing alerts; +https://github.com/cherrymcgerry/scharrelaar)"
)

// Site is one marketplace.
type Site struct {
	ID     string
	Name   string
	Domain string // e.g. "www.2dehands.be"
	// L1 holds the top-level categories. They are stable but not identical between
	// sites ("Huis en Inrichting" is 504 on both, "Auto diversen" is 48 on both),
	// so callers match by name and fall back to id.
	L1 []Category
}

// Category is one node of the category tree.
type Category struct {
	ID       int
	Name     string
	ParentID int // 0 for a top-level category
}

// TwoDeHands and Marktplaats are the supported sites. Their top-level category ids
// were captured from the homepage's window.__CONFIG__.categoryLinks.
var (
	TwoDeHands = Site{
		ID: "2dehands", Name: "2dehands", Domain: "www.2dehands.be",
		L1: []Category{
			{1, "Antiek en Kunst", 0}, {31, "Audio, Tv en Foto", 0}, {48, "Auto diversen", 0},
			{91, "Auto's", 0}, {2600, "Auto-onderdelen", 0}, {201, "Boeken", 0},
			{289, "Caravans en Kamperen", 0}, {1744, "Cd's en Dvd's", 0}, {322, "Computers en Software", 0},
			{378, "Contacten en Berichten", 0}, {1098, "Diensten en Vakmensen", 0}, {395, "Dieren en Toebehoren", 0},
			{239, "Doe-het-zelf en Bouw", 0}, {537, "Elektronische apparatuur", 0}, {445, "Fietsen en Brommers", 0},
			{356, "Games en Spelcomputers", 0}, {1826, "Handtassen en Accessoires", 0}, {1099, "Hobby en Vrije tijd", 0},
			{504, "Huis en Inrichting", 0}, {1032, "Immo", 0}, {565, "Kinderen en Baby's", 0},
			{621, "Kleding | Dames", 0}, {1776, "Kleding | Heren", 0}, {678, "Motoren", 0},
			{728, "Muziek en Instrumenten", 0}, {1784, "Postzegels en Munten", 0}, {784, "Sport en Fitness", 0},
			{820, "Telecommunicatie", 0}, {1984, "Tickets en Kaartjes", 0}, {1847, "Tuin en Terras", 0},
			{167, "Vacatures", 0}, {856, "Vakantie", 0}, {895, "Verzamelen", 0},
			{976, "Watersport en Boten", 0}, {1085, "Zakelijke goederen", 0}, {428, "Diversen", 0},
		},
	}
	Marktplaats = Site{
		ID: "marktplaats", Name: "Marktplaats", Domain: "www.marktplaats.nl",
		L1: []Category{
			{1, "Antiek en Kunst", 0}, {31, "Audio, Tv en Foto", 0}, {91, "Auto's", 0},
			{2600, "Auto-onderdelen", 0}, {48, "Auto diversen", 0}, {201, "Boeken", 0},
			{289, "Caravans en Kamperen", 0}, {1744, "Cd's en Dvd's", 0}, {322, "Computers en Software", 0},
			{378, "Contacten en Berichten", 0}, {1098, "Diensten en Vakmensen", 0}, {395, "Dieren en Toebehoren", 0},
			{239, "Doe-het-zelf en Verbouw", 0}, {445, "Fietsen en Brommers", 0}, {1099, "Hobby en Vrije tijd", 0},
			{504, "Huis en Inrichting", 0}, {1032, "Huizen en Kamers", 0}, {565, "Kinderen en Baby's", 0},
			{621, "Kleding | Dames", 0}, {1776, "Kleding | Heren", 0}, {678, "Motoren", 0},
			{728, "Muziek en Instrumenten", 0}, {1784, "Postzegels en Munten", 0}, {1826, "Sieraden, Tassen en Uiterlijk", 0},
			{356, "Spelcomputers en Games", 0}, {784, "Sport en Fitness", 0}, {820, "Telecommunicatie", 0},
			{1984, "Tickets en Kaartjes", 0}, {1847, "Tuin en Terras", 0}, {167, "Vacatures", 0},
			{856, "Vakantie", 0}, {895, "Verzamelen", 0}, {976, "Watersport en Boten", 0},
			{537, "Witgoed en Apparatuur", 0}, {1085, "Zakelijke goederen", 0}, {428, "Diversen", 0},
		},
	}
)

// Sites lists every supported site.
func Sites() []Site { return []Site{TwoDeHands, Marktplaats} }

// SiteByID returns the site with that id, or false.
func SiteByID(id string) (Site, bool) {
	for _, s := range Sites() {
		if s.ID == id {
			return s, true
		}
	}
	return Site{}, false
}

// L1ByName finds a top-level category by case-insensitive name. A name used by
// another site ("Elektronische apparatuur" on 2dehands is "Witgoed en Apparatuur"
// on Marktplaats) resolves through the shared id.
func (s Site) L1ByName(name string) (Category, bool) {
	for _, c := range s.L1 {
		if strings.EqualFold(c.Name, name) {
			return c, true
		}
	}
	for _, other := range Sites() {
		for _, c := range other.L1 {
			if strings.EqualFold(c.Name, name) {
				return s.L1ByID(c.ID)
			}
		}
	}
	return Category{}, false
}

// L1ByID finds a top-level category by id.
func (s Site) L1ByID(id int) (Category, bool) {
	for _, c := range s.L1 {
		if c.ID == id {
			return c, true
		}
	}
	return Category{}, false
}

// L1ByKey finds a top-level category by its URL key, e.g. "auto-diversen".
func (s Site) L1ByKey(key string) (Category, bool) {
	for _, c := range s.L1 {
		if slug(c.Name) == key {
			return c, true
		}
	}
	return Category{}, false
}

// slug turns a category name into the key the sites use in URLs:
// "Cd's en Dvd's" becomes "cd-s-en-dvd-s".
func slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// l1Key reads the top-level category key from a listing path such as
// "/v/auto-diversen/aanhangers/m123-titel".
func l1Key(vipURL string) string {
	parts := strings.Split(strings.Trim(vipURL, "/"), "/")
	if len(parts) >= 2 && parts[0] == "v" {
		return parts[1]
	}
	return ""
}

// Listing is one advertisement.
type Listing struct {
	ItemID      string
	Title       string
	Description string
	PriceCents  int
	PriceType   string // FIXED, MIN_BID, FAST_BID, SEE_DESCRIPTION, FREE, RESERVED
	CategoryID  int
	L1Key       string // top-level category from the URL, e.g. "auto-diversen"
	Condition   string
	Delivery    string
	City        string
	DistanceM   int // -1 when the site did not report one
	URL         string
	ImageURL    string
	// Photos are the gallery's image URLs, filled by FetchDetails.
	Photos     []string
	Date       string
	Seller     string
	BidCount   int
	HighestBid int // cents, 0 when there are no bids
	Site       string
	// DetailFetched is true once FetchDetails filled the full description and bids.
	DetailFetched bool
}

// PriceEUR is the asking price in euros.
func (l Listing) PriceEUR() float64 { return float64(l.PriceCents) / 100 }

// PriceLabel is a short Dutch description of the asking price.
func (l Listing) PriceLabel() string {
	switch l.PriceType {
	case "FIXED":
		return fmt.Sprintf("€%.0f", l.PriceEUR())
	case "MIN_BID":
		return fmt.Sprintf("bieden vanaf €%.0f", l.PriceEUR())
	case "FREE":
		return "gratis"
	case "FAST_BID":
		return "bieden"
	case "SEE_DESCRIPTION":
		return "zie omschrijving"
	case "RESERVED":
		return "gereserveerd"
	default:
		return strings.ToLower(l.PriceType)
	}
}

// Query is one search.
type Query struct {
	L1Category int
	// L2Categories limits the search to these subcategories of L1Category.
	L2Categories []int
	Keyword      string
	// Attribute filters, e.g. "condition:Niet werkend".
	Attributes []string
	Postcode   string
	DistanceM  int
	Limit      int
}

// RateLimited is returned when the site answers 403 or 429.
type RateLimited struct{ Status int }

func (e *RateLimited) Error() string { return fmt.Sprintf("rate limited (HTTP %d)", e.Status) }

// Client fetches listings from one site.
type Client struct {
	Site    Site
	HTTP    *http.Client
	BaseURL string // override for tests; defaults to https://<domain>
}

// resolve rewrites an absolute listing URL onto BaseURL, so tests can serve detail pages.
func (c *Client) resolve(rawURL string) string {
	if c.BaseURL == "" {
		return rawURL
	}
	if i := strings.Index(rawURL, "://"); i >= 0 {
		if j := strings.Index(rawURL[i+3:], "/"); j >= 0 {
			return c.BaseURL + rawURL[i+3+j:]
		}
	}
	return rawURL
}

func (c *Client) base() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return "https://" + c.Site.Domain
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func (c *Client) get(ctx context.Context, rawURL string) ([]byte, error) {
	var last error
	for attempt := 1; attempt <= 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept-Language", "nl-BE,nl;q=0.9")
		resp, err := c.httpClient().Do(req)
		if err != nil {
			last = err
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			return nil, &RateLimited{Status: resp.StatusCode}
		}
		if resp.StatusCode >= 500 && attempt < 3 {
			last = fmt.Errorf("HTTP %d from %s", resp.StatusCode, rawURL)
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, rawURL)
		}
		return body, nil
	}
	return nil, last
}

// Search runs one query and returns the listings on the first page.
func (c *Client) Search(ctx context.Context, q Query) ([]Listing, error) {
	page, err := c.SearchPage(ctx, q)
	return page.Listings, err
}

// Page is the first page of a search with the counts the site reports for all
// matching listings.
type Page struct {
	Listings []Listing
	Total    int
	// Subcategories says how many matching listings each subcategory holds, largest
	// first.
	Subcategories []CategoryCount
}

// CategoryCount is a subcategory with the number of listings in it.
type CategoryCount struct {
	Category
	Count int
}

// SearchPage runs one query and returns the first page with its counts.
func (c *Client) SearchPage(ctx context.Context, q Query) (Page, error) {
	limit := q.Limit
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	v := url.Values{}
	v.Set("limit", strconv.Itoa(limit))
	v.Set("offset", "0")
	v.Set("sortBy", "SORT_INDEX")
	v.Set("sortOrder", "DECREASING")
	if q.L1Category > 0 {
		v.Set("l1CategoryId", strconv.Itoa(q.L1Category))
		// The site ignores the singular l2CategoryId and rejects a comma-separated list.
		for _, id := range q.L2Categories {
			v.Add("l2CategoryIds", strconv.Itoa(id))
		}
	}
	if q.Keyword != "" {
		v.Set("query", q.Keyword)
	}
	for _, a := range q.Attributes {
		v.Add("attributesByKey[]", a)
	}
	if q.Postcode != "" && q.DistanceM > 0 {
		v.Set("postcode", q.Postcode)
		v.Set("distanceMeters", strconv.Itoa(q.DistanceM))
	}
	body, err := c.get(ctx, c.base()+"/lrp/api/search?"+v.Encode())
	if err != nil {
		return Page{}, err
	}
	return ParsePage(body, c.Site)
}

type searchResponse struct {
	Listings []struct {
		ItemID      string `json:"itemId"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Specific    string `json:"categorySpecificDescription"`
		PriceInfo   struct {
			PriceCents int    `json:"priceCents"`
			PriceType  string `json:"priceType"`
		} `json:"priceInfo"`
		Location struct {
			CityName       string `json:"cityName"`
			DistanceMeters int    `json:"distanceMeters"`
		} `json:"location"`
		Date              string   `json:"date"`
		CategoryID        int      `json:"categoryId"`
		VIPURL            string   `json:"vipUrl"`
		ImageURLs         []string `json:"imageUrls"`
		SellerInformation struct {
			SellerName string `json:"sellerName"`
		} `json:"sellerInformation"`
		Attributes []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"attributes"`
		Pictures []struct {
			LargeURL  string `json:"largeUrl"`
			MediumURL string `json:"mediumUrl"`
		} `json:"pictures"`
	} `json:"listings"`
	SearchCategoryOptions []Category `json:"searchCategoryOptions"`
	TotalResultCount      int        `json:"totalResultCount"`
	Facets                []struct {
		Key        string `json:"key"`
		Categories []struct {
			ID       int    `json:"id"`
			Label    string `json:"label"`
			ParentID int    `json:"parentId"`
			Count    int    `json:"histogramCount"`
		} `json:"categories"`
	} `json:"facets"`
}

// ParseSearch decodes the listings of a /lrp/api/search response.
func ParseSearch(body []byte, site Site) ([]Listing, error) {
	page, err := ParsePage(body, site)
	return page.Listings, err
}

// ParsePage decodes a /lrp/api/search response with its counts.
func ParsePage(body []byte, site Site) (Page, error) {
	var resp searchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return Page{}, fmt.Errorf("decoding search response: %w", err)
	}
	page := Page{Total: resp.TotalResultCount}
	for _, f := range resp.Facets {
		if f.Key != "RelevantCategories" {
			continue
		}
		for _, c := range f.Categories {
			if c.ParentID > 0 && c.Count > 0 {
				page.Subcategories = append(page.Subcategories, CategoryCount{
					Category: Category{ID: c.ID, Name: c.Label, ParentID: c.ParentID}, Count: c.Count})
			}
		}
	}
	sort.SliceStable(page.Subcategories, func(i, j int) bool { return page.Subcategories[i].Count > page.Subcategories[j].Count })
	out := make([]Listing, 0, len(resp.Listings))
	for _, raw := range resp.Listings {
		if raw.ItemID == "" || raw.VIPURL == "" {
			continue
		}
		desc := raw.Specific
		if desc == "" {
			desc = raw.Description
		}
		distance := raw.Location.DistanceMeters
		if distance < 0 {
			distance = -1
		}
		image := ""
		if len(raw.Pictures) > 0 {
			image = raw.Pictures[0].LargeURL
			if image == "" {
				image = raw.Pictures[0].MediumURL
			}
		}
		if image == "" && len(raw.ImageURLs) > 0 {
			image = raw.ImageURLs[0]
		}
		if strings.HasPrefix(image, "//") {
			image = "https:" + image
		}
		l := Listing{
			ItemID: raw.ItemID, Title: strings.TrimSpace(raw.Title), Description: strings.TrimSpace(desc),
			PriceCents: raw.PriceInfo.PriceCents, PriceType: raw.PriceInfo.PriceType,
			CategoryID: raw.CategoryID, L1Key: l1Key(raw.VIPURL), City: raw.Location.CityName, DistanceM: distance,
			URL: "https://" + site.Domain + raw.VIPURL, ImageURL: image,
			Date: raw.Date, Seller: raw.SellerInformation.SellerName, Site: site.ID,
		}
		if l.PriceType == "" {
			l.PriceType = "UNKNOWN"
		}
		for _, a := range raw.Attributes {
			switch a.Key {
			case "condition":
				l.Condition = a.Value
			case "delivery":
				l.Delivery = a.Value
			}
		}
		out = append(out, l)
	}
	page.Listings = out
	return page, nil
}

// Categories returns the subcategories of a top-level category, read from a search
// for that category. The result includes the category itself.
func (c *Client) Categories(ctx context.Context, l1 int) ([]Category, error) {
	v := url.Values{}
	v.Set("l1CategoryId", strconv.Itoa(l1))
	v.Set("limit", "1")
	body, err := c.get(ctx, c.base()+"/lrp/api/search?"+v.Encode())
	if err != nil {
		return nil, err
	}
	var resp searchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decoding categories: %w", err)
	}
	return resp.SearchCategoryOptions, nil
}

var (
	descRE  = regexp.MustCompile(`(?s)data-collapsable="description"[^>]*>(.*?)</div>`)
	tagRE   = regexp.MustCompile(`<[^>]+>`)
	condRE  = regexp.MustCompile(`(?s)Attributes-module-label[^>]*>(?:\s*<[^>]+>)*\s*Conditie\s*<.*?Attributes-module-value[^>]*>(.*?)<`)
	wsRE    = regexp.MustCompile(`[ \t]*\n[ \t]*`)
	spaceRE = regexp.MustCompile(`[ \t]{2,}`)
)

// FetchDetails fills the full description, bids and seller location from the listing page.
func (c *Client) FetchDetails(ctx context.Context, l *Listing) error {
	body, err := c.get(ctx, c.resolve(l.URL))
	if err != nil {
		return err
	}
	html := string(body)
	if m := descRE.FindStringSubmatch(html); m != nil {
		text := stripTags(m[1])
		if text != "" {
			l.Description = text
		}
	}
	if l.Condition == "" {
		if m := condRE.FindStringSubmatch(html); m != nil {
			l.Condition = strings.TrimSpace(stripTags(m[1]))
		}
	}
	if cfg, ok := extractConfig(html); ok {
		applyConfig(l, cfg)
	}
	l.DetailFetched = true
	return nil
}

// ListingFromURL builds a listing from its page alone, for scoring a single URL.
func (c *Client) ListingFromURL(ctx context.Context, rawURL string) (Listing, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return Listing{}, err
	}
	slug := strings.Trim(u.Path, "/")
	if i := strings.LastIndex(slug, "/"); i >= 0 {
		slug = slug[i+1:]
	}
	itemID := slug
	if i := strings.Index(slug, "-"); i > 0 {
		itemID = slug[:i]
	}
	l := Listing{ItemID: itemID, URL: rawURL, Site: c.Site.ID, PriceType: "UNKNOWN", DistanceM: -1}
	if err := c.FetchDetails(ctx, &l); err != nil {
		return Listing{}, err
	}
	return l, nil
}

func stripTags(s string) string {
	s = strings.ReplaceAll(s, "<br>", "\n")
	s = strings.ReplaceAll(s, "<br/>", "\n")
	s = strings.ReplaceAll(s, "<br />", "\n")
	s = tagRE.ReplaceAllString(s, " ")
	s = unescape(s)
	s = spaceRE.ReplaceAllString(s, " ")
	return strings.TrimSpace(wsRE.ReplaceAllString(s, "\n"))
}

func unescape(s string) string {
	r := strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'", "&nbsp;", " ")
	return r.Replace(s)
}

// extractConfig returns the JSON object assigned to window.__CONFIG__.
func extractConfig(html string) (map[string]any, bool) {
	i := strings.Index(html, configMarker)
	if i < 0 {
		return nil, false
	}
	dec := json.NewDecoder(strings.NewReader(html[i+len(configMarker):]))
	var cfg map[string]any
	if err := dec.Decode(&cfg); err != nil {
		return nil, false
	}
	return cfg, true
}

func applyConfig(l *Listing, cfg map[string]any) {
	item, _ := cfg["listing"].(map[string]any)
	if item == nil {
		return
	}
	if l.Title == "" {
		l.Title, _ = item["title"].(string)
	}
	if price, ok := item["priceInfo"].(map[string]any); ok {
		if t, _ := price["priceType"].(string); t != "" {
			l.PriceType = t
			l.PriceCents = int(number(price["priceCents"]))
		}
	}
	if cat, ok := item["category"].(map[string]any); ok && l.CategoryID == 0 {
		l.CategoryID = int(number(cat["id"]))
	}
	if bids, ok := item["bidsInfo"].(map[string]any); ok {
		list, _ := bids["bids"].([]any)
		l.BidCount = len(list)
		highest := 0
		for _, b := range list {
			m, _ := b.(map[string]any)
			if m == nil {
				continue
			}
			v := int(number(m["value"]))
			if v == 0 {
				v = int(number(m["amount"]))
			}
			if v > highest {
				highest = v
			}
		}
		l.HighestBid = highest
	}
	if l.City == "" {
		if seller, ok := item["seller"].(map[string]any); ok {
			if loc, ok := seller["location"].(map[string]any); ok {
				l.City, _ = loc["cityName"].(string)
			}
		}
	}
	if l.Delivery == "" {
		if ship, ok := item["shippingInformation"].(map[string]any); ok {
			if dt, ok := ship["deliveryType"].(map[string]any); ok {
				l.Delivery, _ = dt["attributeValueLabel"].(string)
			}
		}
	}
	if gallery, ok := item["gallery"].(map[string]any); ok {
		urls, _ := gallery["imageUrls"].([]any)
		l.Photos = nil
		for _, u := range urls {
			s, ok := u.(string)
			if !ok || s == "" {
				continue
			}
			// $_85 is the large size, about 730 pixels wide.
			s = strings.Replace(s, "$_#", "$_85", 1)
			if strings.HasPrefix(s, "//") {
				s = "https:" + s
			}
			l.Photos = append(l.Photos, s)
		}
		if l.ImageURL == "" && len(l.Photos) > 0 {
			l.ImageURL = l.Photos[0]
		}
	}
}

// FetchPhotos downloads up to max of the listing's photos. Photos that fail to
// load are left out.
func (c *Client) FetchPhotos(ctx context.Context, l Listing, max int) [][]byte {
	urls := l.Photos
	if len(urls) == 0 && l.ImageURL != "" {
		urls = []string{l.ImageURL}
	}
	var out [][]byte
	for _, u := range urls {
		if len(out) >= max || ctx.Err() != nil {
			break
		}
		body, err := c.get(ctx, c.resolve(u))
		if err != nil || len(body) == 0 || len(body) > 4<<20 || !isImage(body) {
			continue
		}
		out = append(out, body)
	}
	return out
}

func isImage(b []byte) bool {
	ct := http.DetectContentType(b)
	return ct == "image/jpeg" || ct == "image/png"
}

func number(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case json.Number:
		f, _ := n.Float64()
		return f
	default:
		return 0
	}
}
