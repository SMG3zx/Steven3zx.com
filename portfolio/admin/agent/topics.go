package main

import (
	"sort"
	"strings"
)

// topSortedProcesses is how many applications the map card lists.
const topSortedProcesses = 6

// project returns only the parts of the snapshot named in topics, so the admin
// server can forward the sections an open view draws and nothing else. Unknown
// topic names are ignored. collectedAt is always included.
//
// Summary topics (network, applications, services) carry counts and the few
// rows the map card shows. Detail topics (connections, listening, processes,
// serviceList) carry the full lists for a detail panel.
func (s Snapshot) project(topics []string) map[string]any {
	out := map[string]any{"collectedAt": s.CollectedAt}
	for _, t := range topics {
		switch strings.TrimSpace(t) {
		case "host":
			out["host"] = s.Host
		case "cpu":
			out["cpu"] = s.CPU
		case "memory":
			out["memory"] = s.Memory
		case "storage":
			out["storage"] = s.Storage
		case "docker":
			out["docker"] = s.Docker
		case "errors":
			out["errors"] = s.Errors
		case "network":
			out["network"] = s.networkSummary()
		case "connections":
			out["connections"] = s.Network.Connections
		case "listening":
			out["listening"] = s.Network.Listening
		case "applications":
			out["applications"] = s.applicationSummary()
		case "processes":
			out["processes"] = s.Applications
		case "services":
			out["services"] = s.serviceSummary()
		case "serviceList":
			out["serviceList"] = s.Services
		}
	}
	return out
}

type networkSummary struct {
	Interfaces      []Interface `json:"interfaces"`
	ListeningCount  int         `json:"listeningCount"`
	ConnectionCount int         `json:"connectionCount"`
	InboundCount    int         `json:"inboundCount"`
	PublicPorts     []uint32    `json:"publicPorts"`
}

func (s Snapshot) networkSummary() networkSummary {
	out := networkSummary{
		Interfaces:      s.Network.Interfaces,
		ListeningCount:  len(s.Network.Listening),
		ConnectionCount: len(s.Network.Connections),
	}
	for _, c := range s.Network.Connections {
		if c.Inbound {
			out.InboundCount++
		}
	}
	seen := map[uint32]bool{}
	for _, l := range s.Network.Listening {
		if !strings.HasPrefix(l.Protocol, "tcp") || (l.Address != "0.0.0.0" && l.Address != "::") || seen[l.Port] {
			continue
		}
		seen[l.Port] = true
		out.PublicPorts = append(out.PublicPorts, l.Port)
	}
	return out
}

type applicationSummary struct {
	Count int       `json:"count"`
	Top   []Process `json:"top"`
}

func (s Snapshot) applicationSummary() applicationSummary {
	// Copy before sorting so the collector's own slice keeps its order.
	top := append([]Process(nil), s.Applications...)
	sort.Slice(top, func(i, j int) bool { return top[i].CPUPercent > top[j].CPUPercent })
	if len(top) > topSortedProcesses {
		top = top[:topSortedProcesses]
	}
	return applicationSummary{Count: len(s.Applications), Top: top}
}

type serviceSummary struct {
	Count       int       `json:"count"`
	Running     int       `json:"running"`
	AutoStopped []Service `json:"autoStopped"`
}

func (s Snapshot) serviceSummary() serviceSummary {
	out := serviceSummary{Count: len(s.Services)}
	for _, v := range s.Services {
		if v.State == "running" {
			out.Running++
		}
		if v.StartType == "automatic" && v.State == "stopped" {
			out.AutoStopped = append(out.AutoStopped, v)
		}
	}
	return out
}
