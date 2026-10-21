package agentlinktest

import (
	"testing"

	"janus/internal/app"
)

func TestLoopbackPassesTheAgentLinkContract(t *testing.T) {
	Run(t, func(*testing.T) Subject {
		link := NewLoopback()
		return Subject{Link: link, Start: func(_ *testing.T, runtime *app.AgentRuntime) func() {
			link.Serve("agent-1", runtime)
			return func() { link.Unserve("agent-1") }
		}}
	})
}
