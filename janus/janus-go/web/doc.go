// Package web provides Janus's small HTTP routing layer. Apps register handlers
// against method and path patterns, and serve them through net/http handlers.
// Request parsing and response writing stay at the HTTP edge; application
// services are called by the registered handlers.
package web
