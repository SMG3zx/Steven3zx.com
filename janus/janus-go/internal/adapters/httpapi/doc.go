// Package httpapi is the HTTP driving adapter. It turns requests into calls
// on the use cases in internal/app and the results into responses, and owns
// sessions-as-cookies, status codes, HTML pages and the listener lifecycle.
// Build a Server with New, then Start and Shutdown it.
package httpapi
