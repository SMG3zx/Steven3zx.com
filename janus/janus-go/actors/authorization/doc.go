// Package authorization evaluates application permission checks against the
// embedded OPA policy. The policy defaults to denying actions without an
// explicit allow rule. Its prepared query is safe for concurrent use.
package authorization
