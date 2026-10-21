// Package cgi is how a Deployment's module sees an HTTP request and answers it:
// CGI/1.1 (RFC 3875), as WAGI uses it. The request arrives as environment
// variables plus the body on stdin; the module writes header lines, a blank
// line and the body to stdout.
package cgi

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/textproto"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Request is the part of an HTTP request a module is told about. Header keys
// may be in any case. Host may carry a port.
type Request struct {
	Method, Path, RawQuery string
	Host, RemoteAddr       string
	Header                 map[string][]string
	ContentLength          int
}

// Response is a module's answer. Header keys are canonical, such as
// "Content-Type".
type Response struct {
	Status int
	Header map[string][]string
	Body   []byte
}

// ErrMalformedResponse is returned for output that is not a CGI response.
var ErrMalformedResponse = errors.New("the module's output is not a CGI response: write header lines, a blank line, then the body")

// Env returns the CGI environment for r, as "NAME=value" entries in a fixed
// order. Each request header becomes HTTP_<NAME>, its values joined with ", ",
// except Content-Type and Content-Length, which have their own variables, and
// Proxy, which a module could mistake for its proxy setting.
func Env(r Request) []string {
	host, port := r.Host, "80"
	if h, p, err := net.SplitHostPort(r.Host); err == nil {
		host, port = h, p
	}
	path := r.Path
	if path == "" {
		path = "/"
	}
	uri := (&url.URL{Path: path, RawQuery: r.RawQuery}).RequestURI()
	env := []string{
		"GATEWAY_INTERFACE=CGI/1.1",
		"SERVER_PROTOCOL=HTTP/1.1",
		"SERVER_SOFTWARE=janus-agent",
		"SERVER_NAME=" + host,
		"SERVER_PORT=" + port,
		"REQUEST_METHOD=" + r.Method,
		"SCRIPT_NAME=",
		"PATH_INFO=" + path,
		"QUERY_STRING=" + r.RawQuery,
		"REQUEST_URI=" + uri,
		"REMOTE_ADDR=" + r.RemoteAddr,
	}
	if r.ContentLength > 0 {
		env = append(env, "CONTENT_LENGTH="+strconv.Itoa(r.ContentLength))
	}
	names := make([]string, 0, len(r.Header))
	for name := range r.Header {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		values := r.Header[name]
		switch textproto.CanonicalMIMEHeaderKey(name) {
		case "Content-Type":
			if len(values) > 0 {
				env = append(env, "CONTENT_TYPE="+values[0])
			}
		case "Content-Length", "Proxy":
		default:
			key := "HTTP_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
			env = append(env, key+"="+strings.Join(values, ", "))
		}
	}
	return env
}

// ParseResponse reads a module's stdout as a CGI response. A Status header sets
// the status; a Location header without one means 302; otherwise it is 200.
// Lines may end in CRLF or LF.
func ParseResponse(out []byte) (Response, error) {
	head, body, found := cutHeader(out)
	if !found {
		return Response{}, ErrMalformedResponse
	}
	response := Response{Status: 200, Header: map[string][]string{}, Body: body}
	status := ""
	for _, line := range strings.Split(strings.ReplaceAll(string(head), "\r\n", "\n"), "\n") {
		name, value, ok := strings.Cut(line, ":")
		name = strings.TrimSpace(name)
		if !ok || name == "" || strings.ContainsAny(name, " \t") {
			return Response{}, fmt.Errorf("%w (bad header line %q)", ErrMalformedResponse, line)
		}
		key := textproto.CanonicalMIMEHeaderKey(name)
		value = strings.TrimSpace(value)
		if key == "Status" {
			status = value
			continue
		}
		response.Header[key] = append(response.Header[key], value)
	}
	switch {
	case status != "":
		code, _, _ := strings.Cut(status, " ")
		n, err := strconv.Atoi(code)
		if err != nil || n < 100 || n > 599 {
			return Response{}, fmt.Errorf("%w (bad Status %q)", ErrMalformedResponse, status)
		}
		response.Status = n
	case len(response.Header["Location"]) > 0:
		response.Status = 302
	}
	return response, nil
}

// cutHeader splits out at the first blank line. The header block must not be
// empty unless the output starts with the blank line.
func cutHeader(out []byte) (head, body []byte, found bool) {
	for i := 0; i < len(out); i++ {
		if out[i] != '\n' {
			continue
		}
		next := out[i+1:]
		switch {
		case bytes.HasPrefix(next, []byte("\r\n")):
			return bytes.TrimRight(out[:i], "\r"), next[2:], true
		case bytes.HasPrefix(next, []byte("\n")):
			return bytes.TrimRight(out[:i], "\r"), next[1:], true
		}
	}
	return nil, nil, false
}
