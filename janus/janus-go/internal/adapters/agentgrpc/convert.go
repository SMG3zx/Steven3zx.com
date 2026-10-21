package agentgrpc

import (
	"time"

	"janus/internal/adapters/agentgrpc/agentpb"
	"janus/internal/app"
	"janus/internal/domain/usage"
)

// maxMessageBytes bounds one message on the stream. A Deploy carries a whole
// module, so it is far above gRPC's 4 MiB default.
const maxMessageBytes = 128 << 20

func commandToProto(id string, command app.AgentCommand) *agentpb.Command {
	out := &agentpb.Command{Id: id}
	switch {
	case command.Deploy != nil:
		d := command.Deploy
		out.Action = &agentpb.Command_Deploy{Deploy: &agentpb.Deploy{DeploymentId: d.ID, Module: d.Module, Sha256: d.SHA256,
			Limits: &agentpb.Limits{Fuel: d.Limits.Fuel, MemoryBytes: d.Limits.MemoryBytes}}}
	case command.Stop != "":
		out.Action = &agentpb.Command_Stop{Stop: &agentpb.Stop{DeploymentId: command.Stop}}
	case command.Pause:
		out.Action = &agentpb.Command_Pause{Pause: &agentpb.Pause{}}
	case command.Resume:
		out.Action = &agentpb.Command_Resume{Resume: &agentpb.Resume{}}
	}
	return out
}

func commandFromProto(in *agentpb.Command) app.AgentCommand {
	switch action := in.Action.(type) {
	case *agentpb.Command_Deploy:
		d := action.Deploy
		return app.AgentCommand{Deploy: &app.HostedDeployment{ID: d.DeploymentId, SHA256: d.Sha256, Module: d.Module,
			Limits: app.RunLimits{Fuel: d.GetLimits().GetFuel(), MemoryBytes: d.GetLimits().GetMemoryBytes()}}}
	case *agentpb.Command_Stop:
		return app.AgentCommand{Stop: action.Stop.DeploymentId}
	case *agentpb.Command_Pause:
		return app.AgentCommand{Pause: true}
	case *agentpb.Command_Resume:
		return app.AgentCommand{Resume: true}
	}
	return app.AgentCommand{}
}

func stateToProto(state app.AgentState) *agentpb.Event {
	out := &agentpb.State{Paused: state.Paused}
	for _, d := range state.Deployments {
		out.Deployments = append(out.Deployments, &agentpb.HostedDeployment{DeploymentId: d.ID, Sha256: d.SHA256})
	}
	return &agentpb.Event{Kind: &agentpb.Event_State{State: out}}
}

func stateFromProto(in *agentpb.State) app.AgentState {
	out := app.AgentState{Paused: in.Paused, Deployments: make([]app.DeploymentHash, 0, len(in.Deployments))}
	for _, d := range in.Deployments {
		out.Deployments = append(out.Deployments, app.DeploymentHash{ID: d.DeploymentId, SHA256: d.Sha256})
	}
	return out
}

func reportToProto(r app.RunReport) *agentpb.Event {
	return &agentpb.Event{Kind: &agentpb.Event_Usage{Usage: &agentpb.RunUsage{
		RunId: r.RunID, DeploymentId: r.DeploymentID, Fuel: r.Usage.Fuel, PeakMemoryBytes: r.Usage.PeakMemoryBytes,
		DurationMs: r.Usage.DurationMS, CpuTimeUs: r.Usage.CPUTimeMicros, NetworkBytesIn: r.NetworkBytesIn,
		NetworkBytesOut: r.NetworkBytesOut, Status: r.Status, Error: r.Error, Probe: r.Probe,
		StartedAtUnixMs: r.StartedAt.UnixMilli(),
	}}}
}

func reportFromProto(in *agentpb.RunUsage) app.RunReport {
	return app.RunReport{
		RunID: in.RunId, DeploymentID: in.DeploymentId,
		Usage:          usage.Usage{Fuel: in.Fuel, PeakMemoryBytes: in.PeakMemoryBytes, DurationMS: in.DurationMs, CPUTimeMicros: in.CpuTimeUs},
		NetworkBytesIn: in.NetworkBytesIn, NetworkBytesOut: in.NetworkBytesOut, Status: in.Status, Error: in.Error,
		Probe: in.Probe, StartedAt: time.UnixMilli(in.StartedAtUnixMs).UTC(),
	}
}
