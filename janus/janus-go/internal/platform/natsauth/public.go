package natsauth

// PublicTrust is the public half of the hub's NATS trust: the operator and the
// accounts an Agent's own server verifies its leaf connection against. None of
// it is secret.
type PublicTrust struct {
	OperatorJWT   string
	SystemAccount string
	SystemJWT     string
	AgentsAccount string
	AgentsJWT     string
}
