// Package agentlive holds the live test of the Janus Agent (ENG-111): the real
// janus-agent binary on a real Headscale tailnet, driven by the control plane's
// HTTP API and use cases. It runs only with the live build tag:
//
//	go test -tags live ./e2e/agentlive
//
// It needs Docker, cgo and internet access for Headscale's DERP map.
package agentlive
