// Package gateway is the HTTP service. It turns requests into calls on the
// other services over NATS and the results into responses, and owns
// sessions-as-cookies, status codes, HTML pages and the listener lifecycle.
// Build a Server with New, then Start and Shutdown it.
package gateway
