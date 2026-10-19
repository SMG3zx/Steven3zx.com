package config

import "time"

type LoadEnvironment struct{}

type EnvironmentLoaded struct{ Err error }

type Load struct{}

type Loaded struct {
	Config Config
	Err    error
}

// Config contains validated runtime settings loaded from the environment.
type Config struct {
	HTTPAddress            string
	DatabasePath           string
	JWTSecret              string
	JWTIssuer              string
	JWTExpiry              time.Duration
	CookieSecure           bool
	GoogleClientID         string
	GoogleSecret           string
	GoogleCallback         string
	GoogleEnabled          bool
	GitHubClientID         string
	GitHubSecret           string
	GitHubCallback         string
	GitHubEnabled          bool
	WASMBuilderImage       string
	GoWASMBuilderImage     string
	JSWASMBuilderImage     string
	PyroscopeServerAddress string
}
