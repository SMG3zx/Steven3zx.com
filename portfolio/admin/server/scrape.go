package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// scrapedJob holds the fields a job posting advertises. Empty means the page
// did not say; the form leaves those fields alone.
type scrapedJob struct {
	Company  string `json:"company"`
	Title    string `json:"title"`
	Location string `json:"location"`
	Salary   string `json:"salary"`
}

const maxScrapeBody = 2 << 20

var errBlockedAddress = errors.New("address is not a public host")

// scrapeClient fetches postings. Its dialer checks every address it connects
// to, so a redirect or a DNS name that resolves inward cannot reach the
// host's own network.
var scrapeClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		DialContext:           publicDial,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 8 * time.Second,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("too many redirects")
		}
		return checkScrapeURL(req.URL)
	},
}

func checkScrapeURL(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("url must be http or https")
	}
	if u.Host == "" {
		return errors.New("url has no host")
	}
	return nil
}

// publicDial resolves host and connects to the first public address, so the
// address that was checked is the one that is dialed.
func publicDial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	var d net.Dialer
	for _, ip := range ips {
		if blockedIP(ip.IP) {
			continue
		}
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if err == nil {
			return conn, nil
		}
	}
	return nil, errBlockedAddress
}

// blockedIP reports whether an address is one the scraper must never fetch:
// loopback, private, link-local, multicast, unspecified, or carrier-grade NAT.
func blockedIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1]&0xc0 == 64 { // 100.64.0.0/10
		return true
	}
	return false
}

// scrapeJob fetches a posting page and returns the fields it advertises.
func scrapeJob(ctx context.Context, raw string) (scrapedJob, error) {
	u, err := url.Parse(raw)
	if err != nil || checkScrapeURL(u) != nil {
		return scrapedJob{}, errors.New("url must be an http or https link")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return scrapedJob{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; steven3zx-admin/1.0)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := scrapeClient.Do(req)
	if err != nil {
		if errors.Is(err, errBlockedAddress) {
			return scrapedJob{}, errors.New("that address is not a public host")
		}
		return scrapedJob{}, fmt.Errorf("could not fetch the page: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return scrapedJob{}, fmt.Errorf("the page returned %s", resp.Status)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.Contains(ct, "html") {
		return scrapedJob{}, errors.New("the page is not HTML")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxScrapeBody))
	if err != nil {
		return scrapedJob{}, fmt.Errorf("read page: %w", err)
	}
	return parsePosting(string(body)), nil
}

var (
	ldJSONRe = regexp.MustCompile(`(?is)<script[^>]*type\s*=\s*["']application/ld\+json["'][^>]*>(.*?)</script>`)
	metaRe   = regexp.MustCompile(`(?is)<meta\s[^>]*>`)
	attrRe   = regexp.MustCompile(`(?is)([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	titleRe  = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
)

// parsePosting reads JobPosting JSON-LD first, since most job boards publish
// it, then falls back to Open Graph and plain meta tags.
func parsePosting(page string) scrapedJob {
	var out scrapedJob
	for _, m := range ldJSONRe.FindAllStringSubmatch(page, -1) {
		var doc any
		if err := json.Unmarshal([]byte(strings.TrimSpace(m[1])), &doc); err != nil {
			continue
		}
		for _, posting := range jobPostings(doc) {
			fillFromPosting(&out, posting)
		}
	}

	meta := map[string]string{}
	for _, tag := range metaRe.FindAllString(page, -1) {
		attrs := map[string]string{}
		for _, a := range attrRe.FindAllStringSubmatch(tag, -1) {
			attrs[strings.ToLower(a[1])] = html.UnescapeString(a[2] + a[3])
		}
		key := attrs["property"]
		if key == "" {
			key = attrs["name"]
		}
		if key != "" && attrs["content"] != "" {
			meta[strings.ToLower(key)] = strings.TrimSpace(attrs["content"])
		}
	}
	if out.Company == "" {
		out.Company = meta["og:site_name"]
	}
	if out.Title == "" {
		if t := meta["og:title"]; t != "" {
			out.Title = t
		} else if m := titleRe.FindStringSubmatch(page); m != nil {
			out.Title = strings.TrimSpace(html.UnescapeString(m[1]))
		}
	}
	if out.Location == "" {
		out.Location = meta["job:location"]
	}
	return cleanScraped(out)
}

// jobPostings collects every JobPosting object in a JSON-LD document.
func jobPostings(v any) []map[string]any {
	var found []map[string]any
	switch t := v.(type) {
	case []any:
		for _, item := range t {
			found = append(found, jobPostings(item)...)
		}
	case map[string]any:
		if hasType(t["@type"], "JobPosting") {
			found = append(found, t)
		}
		if graph, ok := t["@graph"]; ok {
			found = append(found, jobPostings(graph)...)
		}
	}
	return found
}

func hasType(v any, want string) bool {
	switch t := v.(type) {
	case string:
		return t == want
	case []any:
		for _, item := range t {
			if s, ok := item.(string); ok && s == want {
				return true
			}
		}
	}
	return false
}

// fillFromPosting copies the fields of one JobPosting that are still empty.
func fillFromPosting(out *scrapedJob, p map[string]any) {
	if out.Title == "" {
		out.Title = textOf(p["title"])
	}
	if out.Company == "" {
		out.Company = textOf(p["hiringOrganization"])
	}
	if out.Location == "" {
		out.Location = locationOf(p)
	}
	if out.Salary == "" {
		out.Salary = salaryOf(p["baseSalary"])
	}
}

// textOf reads a string, or the name of an object such as an organization.
func textOf(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case map[string]any:
		return textOf(t["name"])
	case []any:
		if len(t) > 0 {
			return textOf(t[0])
		}
	}
	return ""
}

// firstObject returns the first object in v, which may be a single object or a list.
func firstObject(v any) map[string]any {
	switch t := v.(type) {
	case map[string]any:
		return t
	case []any:
		if len(t) > 0 {
			m, _ := t[0].(map[string]any)
			return m
		}
	}
	return nil
}

func locationOf(p map[string]any) string {
	if loc := firstObject(p["jobLocation"]); loc != nil {
		if addr, ok := loc["address"].(map[string]any); ok {
			var parts []string
			for _, k := range []string{"addressLocality", "addressRegion", "addressCountry"} {
				if s := textOf(addr[k]); s != "" {
					parts = append(parts, s)
				}
			}
			if len(parts) > 0 {
				return strings.Join(parts, ", ")
			}
		}
	}
	if s, _ := p["jobLocationType"].(string); s == "TELECOMMUTE" {
		return "Remote"
	}
	return ""
}

// salaryOf renders a schema.org baseSalary such as {"currency":"USD",
// "value":{"minValue":120000,"maxValue":140000,"unitText":"YEAR"}}.
func salaryOf(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	currency, _ := m["currency"].(string)
	var amount, unit string
	switch t := m["value"].(type) {
	case float64:
		amount = fmt.Sprintf("%.0f", t)
	case map[string]any:
		lo, hasLo := t["minValue"].(float64)
		hi, hasHi := t["maxValue"].(float64)
		switch {
		case hasLo && hasHi:
			amount = fmt.Sprintf("%.0f–%.0f", lo, hi)
		case hasLo:
			amount = fmt.Sprintf("%.0f", lo)
		case hasHi:
			amount = fmt.Sprintf("%.0f", hi)
		}
		if n, ok := t["value"].(float64); ok && amount == "" {
			amount = fmt.Sprintf("%.0f", n)
		}
		unit, _ = t["unitText"].(string)
	}
	if amount == "" {
		return ""
	}
	out := amount
	if currency != "" {
		out = currency + " " + out
	}
	if unit != "" {
		out += " / " + strings.ToLower(unit)
	}
	return out
}

// cleanScraped collapses whitespace and caps each field to the form's limit.
func cleanScraped(s scrapedJob) scrapedJob {
	clip := func(v string) string {
		v = strings.Join(strings.Fields(v), " ")
		if len(v) > maxJobShort {
			v = v[:maxJobShort]
		}
		return v
	}
	return scrapedJob{
		Company:  clip(s.Company),
		Title:    clip(s.Title),
		Location: clip(s.Location),
		Salary:   clip(s.Salary),
	}
}
