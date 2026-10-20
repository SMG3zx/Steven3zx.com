package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const jsonLDPosting = `<html><head><title>Ignored</title>
<script type="application/ld+json">
{"@context":"https://schema.org","@type":"JobPosting",
 "title":"Senior Backend Engineer",
 "hiringOrganization":{"@type":"Organization","name":"Acme Robotics"},
 "jobLocation":{"@type":"Place","address":{"@type":"PostalAddress",
   "addressLocality":"Austin","addressRegion":"TX","addressCountry":"US"}},
 "baseSalary":{"@type":"MonetaryAmount","currency":"USD",
   "value":{"@type":"QuantitativeValue","minValue":120000,"maxValue":140000,"unitText":"YEAR"}}}
</script></head><body></body></html>`

func TestParsePostingReadsJobPostingJSONLD(t *testing.T) {
	got := parsePosting(jsonLDPosting)
	want := scrapedJob{
		Company:  "Acme Robotics",
		Title:    "Senior Backend Engineer",
		Location: "Austin, TX, US",
		Salary:   "USD 120000–140000 / year",
	}
	if got != want {
		t.Fatalf("parsePosting = %+v\nwant %+v", got, want)
	}
}

func TestParsePostingFindsPostingInGraphAndRemote(t *testing.T) {
	page := `<script type="application/ld+json">{"@graph":[
		{"@type":"WebSite","name":"Board"},
		{"@type":["Thing","JobPosting"],"title":"Designer","jobLocationType":"TELECOMMUTE",
		 "hiringOrganization":"Globex"}]}</script>`
	got := parsePosting(page)
	if got.Title != "Designer" || got.Company != "Globex" || got.Location != "Remote" {
		t.Fatalf("parsePosting = %+v", got)
	}
}

func TestParsePostingFallsBackToMetaTags(t *testing.T) {
	page := `<html><head>
		<meta content="Platform Engineer &amp; more" property="og:title">
		<meta name="og:site_name" content="Initech">
		<meta property='job:location' content='Berlin'>
		</head></html>`
	got := parsePosting(page)
	if got.Title != "Platform Engineer & more" || got.Company != "Initech" || got.Location != "Berlin" {
		t.Fatalf("parsePosting = %+v", got)
	}
}

func TestParsePostingUsesTitleTagAndSkipsBrokenJSON(t *testing.T) {
	page := `<title> QA Analyst </title><script type="application/ld+json">{not json</script>`
	if got := parsePosting(page); got.Title != "QA Analyst" || got.Company != "" {
		t.Fatalf("parsePosting = %+v", got)
	}
}

func TestBlockedIP(t *testing.T) {
	blocked := []string{"127.0.0.1", "::1", "10.1.2.3", "192.168.0.5", "172.16.9.9", "169.254.1.1",
		"100.64.0.1", "0.0.0.0", "224.0.0.1", "fe80::1", "fd00::1"}
	for _, s := range blocked {
		if !blockedIP(net.ParseIP(s)) {
			t.Errorf("%s should be blocked", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "93.184.216.34", "2606:4700::1111"} {
		if blockedIP(net.ParseIP(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
}

func TestScrapeJobRefusesPrivateAndBadURLs(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// httptest listens on loopback, so a working fetch here would mean the
	// guard is missing.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(jsonLDPosting))
	}))
	defer srv.Close()
	if _, err := scrapeJob(ctx, srv.URL); err == nil {
		t.Fatal("scrapeJob fetched a loopback address")
	}

	for _, bad := range []string{"", "file:///etc/passwd", "ftp://example.com/x", "not a url", "http://"} {
		if _, err := scrapeJob(ctx, bad); err == nil {
			t.Errorf("scrapeJob(%q) succeeded", bad)
		}
	}
}

func TestScrapeEndpointRequiresSignInAndSameOrigin(t *testing.T) {
	srv, token := newJobsTestServer(t)
	h := srv.routes()

	req := httptest.NewRequest(http.MethodPost, "/admin/api/jobs/scrape", strings.NewReader(`{"url":"https://example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("scrape without a session: %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/admin/api/jobs/scrape", strings.NewReader(`{"url":"http://127.0.0.1:1/"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: cookieName, Value: token})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("scrape of a loopback address: %d %s, want 422", rec.Code, rec.Body)
	}
}
