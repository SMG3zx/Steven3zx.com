// Package routes builds the HTTP application and connects request handlers to
// Janus service actors. HTTP request goroutines perform coordination; actor
// Receive turns are not used to hold live request or response objects.
package routes
