package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

const (
	fastInterval    = 2 * time.Second
	processInterval = 5 * time.Second
	dockerInterval  = 5 * time.Second
	slowInterval    = 30 * time.Second
	maxConnections  = 2000
)

// collector samples the host on a few cadences and keeps the latest
// Snapshot ready, so HTTP requests never wait on a slow probe.
type collector struct {
	docker *dockerClient

	mu   sync.RWMutex
	snap Snapshot

	// Previous counters, for turning cumulative totals into rates.
	prevCPU   []cpu.TimesStat
	prevNet   map[string]gnet.IOCountersStat
	prevDisk  map[string]disk.IOCountersStat
	prevFast  time.Time
	procs     map[int32]*process.Process
	procNames map[int32]string
	slowErrs  []string
	fastErrs  []string
}

func newCollector(d *dockerClient) *collector {
	return &collector{
		docker:    d,
		procs:     map[int32]*process.Process{},
		procNames: map[int32]string{},
	}
}

func (c *collector) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s := c.snap
	s.Errors = append(append([]string{}, c.slowErrs...), c.fastErrs...)
	return s
}

func (c *collector) Run(ctx context.Context) {
	c.slow()
	c.processes()
	c.fast()
	go c.loop(ctx, dockerInterval, c.dockerSample)
	go c.loop(ctx, processInterval, c.processes)
	go c.loop(ctx, slowInterval, c.slow)
	c.loop(ctx, fastInterval, c.fast)
}

func (c *collector) loop(ctx context.Context, every time.Duration, fn func()) {
	fn()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn()
		}
	}
}

func (c *collector) slow() {
	var errs []string
	info, err := host.Info()
	if err != nil {
		errs = append(errs, "host: "+err.Error())
	}
	cpus, err := cpu.Info()
	if err != nil {
		errs = append(errs, "cpu info: "+err.Error())
	}
	physical, _ := cpu.Counts(false)
	logical, _ := cpu.Counts(true)
	services, err := listServices()
	if err != nil {
		errs = append(errs, "services: "+err.Error())
	}
	sort.Slice(services, func(i, j int) bool {
		return strings.ToLower(services[i].DisplayName) < strings.ToLower(services[j].DisplayName)
	})

	c.mu.Lock()
	defer c.mu.Unlock()
	if info != nil {
		c.snap.Host = HostInfo{
			Hostname:        info.Hostname,
			OS:              info.OS,
			Platform:        info.Platform,
			PlatformVersion: info.PlatformVersion,
			KernelVersion:   info.KernelVersion,
			BootTime:        info.BootTime,
		}
	}
	if len(cpus) > 0 {
		c.snap.CPU.Model = strings.TrimSpace(cpus[0].ModelName)
		c.snap.CPU.MHz = cpus[0].Mhz
	}
	c.snap.CPU.PhysicalCores = physical
	c.snap.CPU.LogicalCores = logical
	if services != nil {
		c.snap.Services = services
	}
	c.slowErrs = errs
}

func (c *collector) fast() {
	now := time.Now()
	var errs []string

	times, err := cpu.Times(true)
	if err != nil {
		errs = append(errs, "cpu: "+err.Error())
	}
	vm, err := mem.VirtualMemory()
	if err != nil {
		errs = append(errs, "memory: "+err.Error())
	}
	swap, _ := mem.SwapMemory()
	netCounters, err := gnet.IOCounters(true)
	if err != nil {
		errs = append(errs, "net io: "+err.Error())
	}
	ifaces, _ := gnet.Interfaces()
	conns, err := gnet.Connections("inet")
	if err != nil {
		errs = append(errs, "connections: "+err.Error())
	}
	parts, err := disk.Partitions(false)
	if err != nil {
		errs = append(errs, "partitions: "+err.Error())
	}
	diskIO, _ := disk.IOCounters()
	uptime, _ := host.Uptime()

	c.mu.Lock()
	defer c.mu.Unlock()
	elapsed := now.Sub(c.prevFast).Seconds()
	if c.prevFast.IsZero() {
		elapsed = 0
	}

	c.snap.CollectedAt = now
	c.snap.Host.UptimeSeconds = uptime
	c.snap.CPU.Cores, c.snap.CPU.TotalPercent = cpuUsage(c.prevCPU, times)
	c.prevCPU = times

	if vm != nil {
		c.snap.Memory = MemoryInfo{
			Total: vm.Total, Used: vm.Used, Available: vm.Available, UsedPercent: vm.UsedPercent,
		}
	}
	if swap != nil {
		c.snap.Memory.SwapTotal = swap.Total
		c.snap.Memory.SwapUsed = swap.Used
		c.snap.Memory.SwapPercent = swap.UsedPercent
	}

	c.snap.Network.Interfaces = c.interfaces(ifaces, netCounters, elapsed)
	c.snap.Network.Listening, c.snap.Network.Connections = c.sockets(conns)
	c.snap.Storage = c.disks(parts, diskIO, elapsed)
	c.prevFast = now
	c.fastErrs = errs
}

func cpuUsage(prev, cur []cpu.TimesStat) ([]CPUCore, float64) {
	cores := make([]CPUCore, len(cur))
	var sum float64
	for i, t := range cur {
		cores[i].ID = i
		if i >= len(prev) {
			continue
		}
		p := prev[i]
		total := cpuTotal(t) - cpuTotal(p)
		if total <= 0 {
			continue
		}
		idle := (t.Idle + t.Iowait) - (p.Idle + p.Iowait)
		cores[i].IdlePercent = clampPct(idle / total * 100)
		cores[i].UserPercent = clampPct((t.User - p.User) / total * 100)
		cores[i].SystemPercent = clampPct((t.System - p.System) / total * 100)
		cores[i].Percent = clampPct(100 - cores[i].IdlePercent)
		sum += cores[i].Percent
	}
	if len(cores) == 0 {
		return cores, 0
	}
	return cores, sum / float64(len(cores))
}

func cpuTotal(t cpu.TimesStat) float64 {
	return t.User + t.System + t.Idle + t.Nice + t.Iowait + t.Irq + t.Softirq + t.Steal
}

func clampPct(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func (c *collector) interfaces(ifaces gnet.InterfaceStatList, counters []gnet.IOCountersStat, elapsed float64) []Interface {
	byName := map[string]gnet.InterfaceStat{}
	for _, i := range ifaces {
		byName[i.Name] = i
	}
	prev := c.prevNet
	c.prevNet = map[string]gnet.IOCountersStat{}

	var out []Interface
	for _, n := range counters {
		c.prevNet[n.Name] = n
		// Skip adapters that have never moved a byte (Teredo, ISATAP, idle virtual NICs).
		if n.BytesSent == 0 && n.BytesRecv == 0 {
			continue
		}
		iface := Interface{
			Name: n.Name, BytesSent: n.BytesSent, BytesRecv: n.BytesRecv,
			PacketsSent: n.PacketsSent, PacketsRecv: n.PacketsRecv,
			Errors: n.Errin + n.Errout, Drops: n.Dropin + n.Dropout,
		}
		if meta, ok := byName[n.Name]; ok {
			for _, a := range meta.Addrs {
				iface.Addresses = append(iface.Addresses, a.Addr)
			}
			for _, f := range meta.Flags {
				if f == "up" {
					iface.Up = true
				}
			}
		}
		if p, ok := prev[n.Name]; ok && elapsed > 0 {
			iface.SendRate = rate(p.BytesSent, n.BytesSent, elapsed)
			iface.RecvRate = rate(p.BytesRecv, n.BytesRecv, elapsed)
		}
		out = append(out, iface)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BytesRecv+out[i].BytesSent > out[j].BytesRecv+out[j].BytesSent })
	return out
}

func rate(prev, cur uint64, elapsed float64) float64 {
	if cur < prev {
		return 0
	}
	return float64(cur-prev) / elapsed
}

func protoName(t uint32, family uint32) string {
	p := "tcp"
	if t == 2 { // SOCK_DGRAM
		p = "udp"
	}
	if family == 23 { // AF_INET6 on Windows
		p += "6"
	}
	return p
}

func (c *collector) sockets(conns []gnet.ConnectionStat) ([]Listener, []Connection) {
	listenPorts := map[uint32]bool{}
	seen := map[string]bool{}
	var listeners []Listener
	for _, s := range conns {
		isUDP := s.Type == 2
		if s.Status != "LISTEN" && !isUDP {
			continue
		}
		proto := protoName(s.Type, s.Family)
		key := fmt.Sprintf("%s|%s|%d", proto, s.Laddr.IP, s.Laddr.Port)
		if seen[key] {
			continue
		}
		seen[key] = true
		if !isUDP {
			listenPorts[s.Laddr.Port] = true
		}
		listeners = append(listeners, Listener{
			Protocol: proto, Address: s.Laddr.IP, Port: s.Laddr.Port,
			PID: s.Pid, Process: c.procNames[s.Pid],
		})
	}
	sort.Slice(listeners, func(i, j int) bool {
		if listeners[i].Port != listeners[j].Port {
			return listeners[i].Port < listeners[j].Port
		}
		return listeners[i].Protocol < listeners[j].Protocol
	})

	var out []Connection
	for _, s := range conns {
		if s.Type == 2 || s.Status == "LISTEN" || s.Raddr.IP == "" {
			continue
		}
		out = append(out, Connection{
			Protocol:  protoName(s.Type, s.Family),
			LocalAddr: s.Laddr.IP, LocalPort: s.Laddr.Port,
			RemoteAddr: s.Raddr.IP, RemotePort: s.Raddr.Port,
			Status: s.Status, PID: s.Pid, Process: c.procNames[s.Pid],
			Inbound: listenPorts[s.Laddr.Port],
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Inbound != out[j].Inbound {
			return out[i].Inbound
		}
		return out[i].LocalPort < out[j].LocalPort
	})
	if len(out) > maxConnections {
		out = out[:maxConnections]
	}
	return listeners, out
}

func (c *collector) disks(parts []disk.PartitionStat, io map[string]disk.IOCountersStat, elapsed float64) []Disk {
	prev := c.prevDisk
	c.prevDisk = io
	var out []Disk
	for _, p := range parts {
		u, err := disk.Usage(p.Mountpoint)
		if err != nil || u.Total == 0 {
			continue
		}
		d := Disk{
			Mount: p.Mountpoint, Device: p.Device, FSType: p.Fstype,
			Total: u.Total, Used: u.Used, Free: u.Free, UsedPercent: u.UsedPercent,
		}
		key := strings.TrimSuffix(p.Mountpoint, `\`)
		if cur, ok := io[key]; ok && elapsed > 0 {
			if old, ok := prev[key]; ok {
				d.ReadRate = rate(old.ReadBytes, cur.ReadBytes, elapsed)
				d.WriteRate = rate(old.WriteBytes, cur.WriteBytes, elapsed)
			}
		}
		out = append(out, d)
	}
	return out
}

func (c *collector) processes() {
	pids, err := process.Pids()
	if err != nil {
		return
	}
	c.mu.RLock()
	logical := c.snap.CPU.LogicalCores
	total := c.snap.Memory.Total
	c.mu.RUnlock()
	if logical == 0 {
		logical = 1
	}

	live := map[int32]bool{}
	names := map[int32]string{}
	var out []Process
	for _, pid := range pids {
		live[pid] = true
		p, ok := c.procs[pid]
		if !ok {
			p, err = process.NewProcess(pid)
			if err != nil {
				continue
			}
			c.procs[pid] = p
		}
		name, err := p.Name()
		if err != nil {
			continue
		}
		names[pid] = name
		pr := Process{PID: pid, Name: name}
		// Percent(0) measures against the previous call on the same *Process;
		// gopsutil reports it per core, so divide to get a share of the machine.
		if pct, err := p.Percent(0); err == nil {
			pr.CPUPercent = pct / float64(logical)
		}
		if m, err := p.MemoryInfo(); err == nil {
			pr.MemoryRSS = m.RSS
			if total > 0 {
				pr.MemPercent = float32(float64(m.RSS) / float64(total) * 100)
			}
		}
		if n, err := p.NumThreads(); err == nil {
			pr.Threads = n
		}
		if t, err := p.CreateTime(); err == nil {
			pr.StartedAt = t
		}
		out = append(out, pr)
	}
	for pid := range c.procs {
		if !live[pid] {
			delete(c.procs, pid)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MemoryRSS > out[j].MemoryRSS })

	c.mu.Lock()
	c.snap.Applications = out
	c.snap.Host.ProcessCount = len(out)
	c.procNames = names
	c.mu.Unlock()
}

func (c *collector) dockerSample() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	d := c.docker.collect(ctx)
	c.mu.Lock()
	c.snap.Docker = d
	c.mu.Unlock()
}
