package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Microsoft/go-winio"
)

// dockerClient talks to the Docker Desktop engine over its named pipe.
type dockerClient struct {
	http *http.Client
	// stream has no overall timeout: log followers stay open until their context ends.
	stream *http.Client
}

func newDockerClient(pipe string) *dockerClient {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return winio.DialPipeContext(ctx, pipe)
		},
	}
	return &dockerClient{
		http:   &http.Client{Timeout: 10 * time.Second, Transport: transport},
		stream: &http.Client{Transport: transport},
	}
}

func (d *dockerClient) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker"+path, nil)
	if err != nil {
		return err
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("docker %s: %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type dockerInfoResp struct {
	ServerVersion     string `json:"ServerVersion"`
	OperatingSystem   string `json:"OperatingSystem"`
	NCPU              int    `json:"NCPU"`
	MemTotal          uint64 `json:"MemTotal"`
	Images            int    `json:"Images"`
	ContainersRunning int    `json:"ContainersRunning"`
	ContainersStopped int    `json:"ContainersStopped"`
}

type dockerContainerResp struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	Image  string            `json:"Image"`
	State  string            `json:"State"`
	Status string            `json:"Status"`
	Labels map[string]string `json:"Labels"`
	Ports  []struct {
		IP          string `json:"IP"`
		PrivatePort int    `json:"PrivatePort"`
		PublicPort  int    `json:"PublicPort"`
		Type        string `json:"Type"`
	} `json:"Ports"`
	NetworkSettings struct {
		Networks map[string]json.RawMessage `json:"Networks"`
	} `json:"NetworkSettings"`
}

type dockerStatsResp struct {
	CPUStats    dockerCPUStats `json:"cpu_stats"`
	PreCPUStats dockerCPUStats `json:"precpu_stats"`
	MemoryStats struct {
		Usage uint64            `json:"usage"`
		Limit uint64            `json:"limit"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`
	Networks map[string]struct {
		RxBytes uint64 `json:"rx_bytes"`
		TxBytes uint64 `json:"tx_bytes"`
	} `json:"networks"`
	BlkioStats struct {
		IOServiceBytesRecursive []struct {
			Op    string `json:"op"`
			Value uint64 `json:"value"`
		} `json:"io_service_bytes_recursive"`
	} `json:"blkio_stats"`
	PidsStats struct {
		Current uint64 `json:"current"`
	} `json:"pids_stats"`
}

type dockerCPUStats struct {
	CPUUsage struct {
		TotalUsage uint64 `json:"total_usage"`
	} `json:"cpu_usage"`
	SystemCPUUsage uint64 `json:"system_cpu_usage"`
	OnlineCPUs     uint64 `json:"online_cpus"`
}

func (d *dockerClient) collect(ctx context.Context) DockerInfo {
	var info dockerInfoResp
	if err := d.get(ctx, "/info", &info); err != nil {
		return DockerInfo{Error: err.Error()}
	}
	var list []dockerContainerResp
	if err := d.get(ctx, "/containers/json?all=1", &list); err != nil {
		return DockerInfo{Error: err.Error()}
	}

	out := DockerInfo{
		Available:         true,
		ServerVersion:     info.ServerVersion,
		OperatingSystem:   info.OperatingSystem,
		CPUs:              info.NCPU,
		MemoryTotal:       info.MemTotal,
		Images:            info.Images,
		ContainersRunning: info.ContainersRunning,
		ContainersStopped: info.ContainersStopped,
		Containers:        make([]Container, len(list)),
	}

	var wg sync.WaitGroup
	for i, c := range list {
		out.Containers[i] = containerFromList(c)
		if c.State != "running" {
			continue
		}
		wg.Add(1)
		go func(dst *Container, id string) {
			defer wg.Done()
			// stream=false waits one sampling interval so precpu_stats is filled in.
			var s dockerStatsResp
			if err := d.get(ctx, "/containers/"+id+"/stats?stream=false", &s); err == nil {
				applyStats(dst, s)
			}
		}(&out.Containers[i], c.ID)
	}
	wg.Wait()

	sort.Slice(out.Containers, func(i, j int) bool {
		a, b := out.Containers[i], out.Containers[j]
		if a.Project != b.Project {
			return a.Project < b.Project
		}
		return a.Name < b.Name
	})
	return out
}

func containerFromList(c dockerContainerResp) Container {
	name := c.ID[:12]
	if len(c.Names) > 0 {
		name = strings.TrimPrefix(c.Names[0], "/")
	}
	ctr := Container{
		ID:      c.ID[:12],
		Name:    name,
		Image:   c.Image,
		State:   c.State,
		Status:  c.Status,
		Project: c.Labels["com.docker.compose.project"],
	}
	for _, p := range c.Ports {
		if p.PublicPort == 0 {
			ctr.Ports = append(ctr.Ports, fmt.Sprintf("%d/%s", p.PrivatePort, p.Type))
			continue
		}
		ctr.Ports = append(ctr.Ports, fmt.Sprintf("%s:%d->%d/%s", p.IP, p.PublicPort, p.PrivatePort, p.Type))
	}
	for n := range c.NetworkSettings.Networks {
		ctr.Networks = append(ctr.Networks, n)
	}
	sort.Strings(ctr.Networks)
	return ctr
}

func applyStats(c *Container, s dockerStatsResp) {
	cpuDelta := float64(s.CPUStats.CPUUsage.TotalUsage) - float64(s.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(s.CPUStats.SystemCPUUsage) - float64(s.PreCPUStats.SystemCPUUsage)
	if cpuDelta > 0 && sysDelta > 0 {
		c.CPUPercent = cpuDelta / sysDelta * float64(s.CPUStats.OnlineCPUs) * 100
	}

	// Match `docker stats`: page cache is reclaimable, so leave it out.
	used := s.MemoryStats.Usage
	if cache, ok := s.MemoryStats.Stats["inactive_file"]; ok && cache < used {
		used -= cache
	}
	c.MemUsage = used
	c.MemLimit = s.MemoryStats.Limit
	if s.MemoryStats.Limit > 0 {
		c.MemPercent = float64(used) / float64(s.MemoryStats.Limit) * 100
	}

	for _, n := range s.Networks {
		c.NetRx += n.RxBytes
		c.NetTx += n.TxBytes
	}
	for _, b := range s.BlkioStats.IOServiceBytesRecursive {
		switch strings.ToLower(b.Op) {
		case "read":
			c.BlockRead += b.Value
		case "write":
			c.BlockWrite += b.Value
		}
	}
	c.PIDs = s.PidsStats.Current
}
