package routes

import "janus/web"

import (
	"time"

	goakt "github.com/tochemey/goakt/v4/actor"

	"janus/actors/authentication"
	"janus/actors/authorization"
)

// Build requests construction of the HTTP application. The actor replies with
// the built application through GoAkt's Ask/Response protocol.
type Build struct{}

// Built is the application returned in response to Build.
type Built struct{ App *web.App }

// Dependencies contains the actors, settings, and provider credentials used
// to construct the route handlers.
type Dependencies struct {
	System         goakt.ActorSystem
	Authentication *authentication.Service
	Authorization  *authorization.Policy
	Accounting     *goakt.PID
	Persistence    *goakt.PID
	Projects       *goakt.PID
	WASMBuilds     *goakt.PID
	WASMRuntime    *goakt.PID
	JWTExpiry      time.Duration
	CookieSecure   bool
	GoogleEnabled  bool
	GoogleClientID string
	GoogleSecret   string
	GoogleCallback string
	JWTSecret      string
	GitHubEnabled  bool
	GitHubClientID string
	GitHubSecret   string
	GitHubCallback string
}
