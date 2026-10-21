package cgi

import (
	"errors"
	"slices"
	"testing"
)

func TestEnvDescribesTheRequest(t *testing.T) {
	env := Env(Request{
		Method: "POST", Path: "/hello world", RawQuery: "a=1&b=2", Host: "abc12345.steven3zx.com:443",
		RemoteAddr: "203.0.113.9", ContentLength: 5,
		Header: map[string][]string{
			"Content-Type":    {"text/plain"},
			"Content-Length":  {"5"},
			"Accept-Language": {"en", "de"},
			"X-Forwarded-For": {"203.0.113.9"},
			"Proxy":           {"http://evil"},
		},
	})
	for _, want := range []string{
		"GATEWAY_INTERFACE=CGI/1.1",
		"SERVER_PROTOCOL=HTTP/1.1",
		"REQUEST_METHOD=POST",
		"PATH_INFO=/hello world",
		"SCRIPT_NAME=",
		"QUERY_STRING=a=1&b=2",
		"REQUEST_URI=/hello%20world?a=1&b=2",
		"SERVER_NAME=abc12345.steven3zx.com",
		"SERVER_PORT=443",
		"REMOTE_ADDR=203.0.113.9",
		"CONTENT_TYPE=text/plain",
		"CONTENT_LENGTH=5",
		"HTTP_ACCEPT_LANGUAGE=en, de",
		"HTTP_X_FORWARDED_FOR=203.0.113.9",
	} {
		if !slices.Contains(env, want) {
			t.Errorf("missing %q in %q", want, env)
		}
	}
	for _, entry := range env {
		for _, banned := range []string{"HTTP_CONTENT_TYPE=", "HTTP_CONTENT_LENGTH=", "HTTP_PROXY="} {
			if len(entry) >= len(banned) && entry[:len(banned)] == banned {
				t.Errorf("env has %q", entry)
			}
		}
	}
}

func TestEnvWithoutABodyHasNoContentLength(t *testing.T) {
	env := Env(Request{Method: "GET", Path: "/", Host: "example.test"})
	for _, entry := range env {
		if entry == "CONTENT_LENGTH=0" || entry[:min(len(entry), 15)] == "CONTENT_LENGTH=" {
			t.Errorf("env has %q", entry)
		}
	}
	if !slices.Contains(env, "SERVER_PORT=80") || !slices.Contains(env, "PATH_INFO=/") {
		t.Errorf("env = %q", env)
	}
}

func TestParseResponseReadsStatusHeadersAndBody(t *testing.T) {
	got, err := ParseResponse([]byte("Status: 404 Not Found\r\nContent-Type: text/plain\r\nX-Thing: a\r\nX-Thing: b\r\n\r\nno such page"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != 404 || string(got.Body) != "no such page" {
		t.Fatalf("got %d %q", got.Status, got.Body)
	}
	if got.Header["Content-Type"][0] != "text/plain" || !slices.Equal(got.Header["X-Thing"], []string{"a", "b"}) {
		t.Fatalf("header = %v", got.Header)
	}
	if _, ok := got.Header["Status"]; ok {
		t.Fatal("the Status header was passed on")
	}
}

func TestParseResponseAcceptsBareNewlines(t *testing.T) {
	got, err := ParseResponse([]byte("content-type: text/html\n\n<p>hi</p>\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != 200 || got.Header["Content-Type"][0] != "text/html" || string(got.Body) != "<p>hi</p>\n" {
		t.Fatalf("got %d %v %q", got.Status, got.Header, got.Body)
	}
}

func TestParseResponseRedirectsOnLocation(t *testing.T) {
	got, err := ParseResponse([]byte("Location: /elsewhere\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != 302 || got.Header["Location"][0] != "/elsewhere" {
		t.Fatalf("got %d %v", got.Status, got.Header)
	}
}

func TestParseResponseRefusesOutputWithoutHeaders(t *testing.T) {
	for _, out := range []string{"", "just a body with no header block", "Status: 200\nContent-Type: text/plain", "not a header\n\nbody", "Status: abc\n\n", "Status: 99\n\n"} {
		if _, err := ParseResponse([]byte(out)); !errors.Is(err, ErrMalformedResponse) {
			t.Errorf("%q: err = %v, want ErrMalformedResponse", out, err)
		}
	}
}
