package agentlinktest

import (
	"fmt"
	"sync/atomic"
	"testing"

	"janus/internal/app"
)

func TestLoopbackPassesTheAgentLinkContract(t *testing.T) {
	var next atomic.Int32
	Run(t, func(*testing.T) Subject {
		link := NewLoopback()
		return Subject{Link: link, Start: func(_ *testing.T, runtime *app.AgentRuntime, address string) (string, func()) {
			if address == "" {
				address = fmt.Sprintf("agent-%d", next.Add(1))
			}
			link.Serve(address, runtime)
			return address, func() { link.Unserve(address) }
		}}
	})
}
