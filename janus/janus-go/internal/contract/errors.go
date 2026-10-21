// Package contract holds what every service's contract shares: the statuses a
// failed request is answered with. Each service's subjects and messages are in
// a package below this one.
package contract

import (
	"fmt"
	"net/http"
)

// The statuses of section 4 of docs/architecture.md.
const (
	StatusInvalid     = 400
	StatusNotFound    = 404
	StatusConflict    = 409
	StatusInternal    = 500
	StatusUnavailable = 503
)

// Error is a failed request. Compare with errors.Is against ErrInvalid,
// ErrNotFound, ErrConflict, ErrInternal or ErrUnavailable: it matches the one
// with its status, whatever the message.
type Error struct {
	Status  int
	Message string
}

// The errors to compare a failure with.
var (
	ErrInvalid     = &Error{Status: StatusInvalid}
	ErrNotFound    = &Error{Status: StatusNotFound}
	ErrConflict    = &Error{Status: StatusConflict}
	ErrInternal    = &Error{Status: StatusInternal}
	ErrUnavailable = &Error{Status: StatusUnavailable}
)

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return http.StatusText(e.Status)
}

// StatusCode is the status the error is answered with.
func (e *Error) StatusCode() int { return e.Status }

// Is matches another *Error with the same status.
func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && other.Status == e.Status
}

// Errorf returns an *Error with status and a formatted message.
func Errorf(status int, format string, args ...any) error {
	return &Error{Status: status, Message: fmt.Sprintf(format, args...)}
}

// FromStatus turns a status and message received from a responder back into an
// *Error. A status this package does not know becomes StatusInternal.
func FromStatus(status int, message string) error {
	switch status {
	case StatusInvalid, StatusNotFound, StatusConflict, StatusUnavailable:
	default:
		status = StatusInternal
	}
	return &Error{Status: status, Message: message}
}
