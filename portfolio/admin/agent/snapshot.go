package main

import "time"

// Snapshot is everything the admin dashboard shows about the origin host.
// The admin server passes it through to the browser unchanged.
type Snapshot struct {
	CollectedAt  time.Time   `json:"collectedAt"`
	Host         HostInfo    `json:"host"`
	CPU          CPUInfo     `json:"cpu"`
	Memory       MemoryInfo  `json:"memory"`
	Network      NetworkInfo `json:"network"`
	Storage      []Disk      `json:"storage"`
	Applications []Process   `json:"applications"`
	Services     []Service   `json:"services"`
	Docker       DockerInfo  `json:"docker"`
	Errors       []string    `json:"errors,omitempty"`
}

type HostInfo struct {
	Hostname        string `json:"hostname"`
	OS              string `json:"os"`
	Platform        string `json:"platform"`
	PlatformVersion string `json:"platformVersion"`
	KernelVersion   string `json:"kernelVersion"`
	UptimeSeconds   uint64 `json:"uptimeSeconds"`
	BootTime        uint64 `json:"bootTime"`
	ProcessCount    int    `json:"processCount"`
}

type CPUInfo struct {
	Model         string    `json:"model"`
	PhysicalCores int       `json:"physicalCores"`
	LogicalCores  int       `json:"logicalCores"`
	MHz           float64   `json:"mhz"`
	TotalPercent  float64   `json:"totalPercent"`
	Cores         []CPUCore `json:"cores"`
}

type CPUCore struct {
	ID            int     `json:"id"`
	Percent       float64 `json:"percent"`
	UserPercent   float64 `json:"userPercent"`
	SystemPercent float64 `json:"systemPercent"`
	IdlePercent   float64 `json:"idlePercent"`
}

type MemoryInfo struct {
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Available   uint64  `json:"available"`
	UsedPercent float64 `json:"usedPercent"`
	SwapTotal   uint64  `json:"swapTotal"`
	SwapUsed    uint64  `json:"swapUsed"`
	SwapPercent float64 `json:"swapPercent"`
}

type NetworkInfo struct {
	Interfaces  []Interface  `json:"interfaces"`
	Listening   []Listener   `json:"listening"`
	Connections []Connection `json:"connections"`
}

type Interface struct {
	Name        string   `json:"name"`
	Addresses   []string `json:"addresses"`
	Up          bool     `json:"up"`
	BytesSent   uint64   `json:"bytesSent"`
	BytesRecv   uint64   `json:"bytesRecv"`
	SendRate    float64  `json:"sendRate"`
	RecvRate    float64  `json:"recvRate"`
	PacketsSent uint64   `json:"packetsSent"`
	PacketsRecv uint64   `json:"packetsRecv"`
	Errors      uint64   `json:"errors"`
	Drops       uint64   `json:"drops"`
}

type Listener struct {
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
	Port     uint32 `json:"port"`
	PID      int32  `json:"pid"`
	Process  string `json:"process"`
}

type Connection struct {
	Protocol   string `json:"protocol"`
	LocalAddr  string `json:"localAddr"`
	LocalPort  uint32 `json:"localPort"`
	RemoteAddr string `json:"remoteAddr"`
	RemotePort uint32 `json:"remotePort"`
	Status     string `json:"status"`
	PID        int32  `json:"pid"`
	Process    string `json:"process"`
	Inbound    bool   `json:"inbound"`
}

type Disk struct {
	Mount       string  `json:"mount"`
	Device      string  `json:"device"`
	FSType      string  `json:"fsType"`
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Free        uint64  `json:"free"`
	UsedPercent float64 `json:"usedPercent"`
	ReadRate    float64 `json:"readRate"`
	WriteRate   float64 `json:"writeRate"`
}

type Process struct {
	PID        int32   `json:"pid"`
	Name       string  `json:"name"`
	CPUPercent float64 `json:"cpuPercent"`
	MemoryRSS  uint64  `json:"memoryRss"`
	MemPercent float32 `json:"memPercent"`
	Threads    int32   `json:"threads"`
	StartedAt  int64   `json:"startedAt"`
}

type Service struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	State       string `json:"state"`
	StartType   string `json:"startType"`
	PID         uint32 `json:"pid"`
}

type DockerInfo struct {
	Available         bool        `json:"available"`
	Error             string      `json:"error,omitempty"`
	ServerVersion     string      `json:"serverVersion"`
	OperatingSystem   string      `json:"operatingSystem"`
	CPUs              int         `json:"cpus"`
	MemoryTotal       uint64      `json:"memoryTotal"`
	Images            int         `json:"images"`
	ContainersRunning int         `json:"containersRunning"`
	ContainersStopped int         `json:"containersStopped"`
	Containers        []Container `json:"containers"`
}

type Container struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Image      string   `json:"image"`
	State      string   `json:"state"`
	Status     string   `json:"status"`
	Project    string   `json:"project"`
	Ports      []string `json:"ports"`
	Networks   []string `json:"networks"`
	CPUPercent float64  `json:"cpuPercent"`
	MemUsage   uint64   `json:"memUsage"`
	MemLimit   uint64   `json:"memLimit"`
	MemPercent float64  `json:"memPercent"`
	NetRx      uint64   `json:"netRx"`
	NetTx      uint64   `json:"netTx"`
	BlockRead  uint64   `json:"blockRead"`
	BlockWrite uint64   `json:"blockWrite"`
	PIDs       uint64   `json:"pids"`
}
