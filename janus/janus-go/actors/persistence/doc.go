// Package persistence owns Janus's SQLite database. Its actor serializes access
// to account, identity, project, import, snapshot, and build records; callers
// communicate with it using the request and result message types in this
// package.
package persistence
