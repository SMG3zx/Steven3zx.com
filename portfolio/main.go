package main

import (
	"embed"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

//go:embed web/*
var web embed.FS

type networkCounter struct{ received, sent uint64 }

type metricPoint struct {
	At              time.Time `json:"at"`
	CPUPercent      float64   `json:"cpuPercent"`
	LogicalCores    int       `json:"logicalCores"`
	MemoryPercent   float64   `json:"memoryPercent"`
	MemoryUsedBytes uint64    `json:"memoryUsedBytes"`
	MemoryTotal     uint64    `json:"memoryTotalBytes"`
	ProcessBytes    uint64    `json:"processBytes"`
	GoRoutines      int       `json:"goRoutines"`
	ProcessCount    int       `json:"processCount"`
	UptimeSeconds   uint64    `json:"uptimeSeconds"`
	ReceiveBytesSec uint64    `json:"receiveBytesPerSecond"`
	SendBytesSec    uint64    `json:"sendBytesPerSecond"`
}

type metricsSampler struct {
	mu       sync.Mutex
	previous networkCounter
	lastAt   time.Time
	started  time.Time
	latest   metricPoint
	ready    bool
}

var metricsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(request *http.Request) bool {
		origin := request.Header.Get("Origin")
		if origin == "" {
			return false
		}
		parsed, err := http.NewRequest(http.MethodGet, origin, nil)
		return err == nil && parsed.URL.Host != "" && strings.EqualFold(parsed.URL.Host, request.Host)
	},
}

func main() {
	gin.SetMode(gin.ReleaseMode)
	assets, err := fs.Sub(web, "web")
	if err != nil {
		log.Fatal(err)
	}
	static := http.FileServer(http.FS(assets))
	sampler := &metricsSampler{started: time.Now()}
	go sampler.run()
	router := gin.New()
	router.Use(gin.Recovery(), securityHeaders())
	router.GET("/healthz", func(c *gin.Context) { c.String(http.StatusOK, "ok\n") })
	router.GET("/ws/metrics", func(c *gin.Context) { serveMetricsSocket(c, sampler) })
	router.GET("/", func(c *gin.Context) { static.ServeHTTP(c.Writer, c.Request) })
	router.GET("/styles.css", func(c *gin.Context) { static.ServeHTTP(c.Writer, c.Request) })
	router.GET("/app.js", func(c *gin.Context) { static.ServeHTTP(c.Writer, c.Request) })
	addr := os.Getenv("PORTFOLIO_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8090"
	}
	log.Printf("portfolio listening on %s", addr)
	if err := router.Run(addr); err != nil {
		log.Fatal(err)
	}
}

func serveMetricsSocket(c *gin.Context, sampler *metricsSampler) {
	connection, err := metricsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer connection.Close()
	connection.SetReadLimit(512)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			if _, _, err := connection.ReadMessage(); err != nil {
				return
			}
		}
	}()
	metricsTicker := time.NewTicker(time.Second)
	defer metricsTicker.Stop()
	pingTicker := time.NewTicker(25 * time.Second)
	defer pingTicker.Stop()
	for {
		select {
		case <-readDone:
			return
		case <-metricsTicker.C:
			point, ready := sampler.current()
			if !ready {
				continue
			}
			if err := connection.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
				return
			}
			if err := connection.WriteJSON(point); err != nil {
				return
			}
		case <-pingTicker.C:
			if err := connection.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
				return
			}
		}
	}
}

func (s *metricsSampler) run() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for range ticker.C {
		point, err := s.sample()
		if err != nil {
			log.Printf("origin metrics sample failed: %v", err)
			continue
		}
		s.mu.Lock()
		s.latest, s.ready = point, true
		s.mu.Unlock()
	}
}

func (s *metricsSampler) current() (metricPoint, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.latest, s.ready
}

func (s *metricsSampler) sample() (metricPoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	point := metricPoint{At: now.UTC(), UptimeSeconds: uint64(time.Since(s.started).Seconds())}
	point.GoRoutines = runtime.NumGoroutine()
	point.LogicalCores = runtime.NumCPU()
	percent, err := cpu.Percent(0, false)
	if err != nil || len(percent) == 0 {
		return point, errors.New("cpu metrics unavailable")
	}
	point.CPUPercent = percent[0]
	vm, err := mem.VirtualMemory()
	if err != nil {
		return point, err
	}
	point.MemoryPercent = vm.UsedPercent
	point.MemoryUsedBytes = vm.Used
	point.MemoryTotal = vm.Total
	proc, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		return point, err
	}
	if info, err := proc.MemoryInfo(); err == nil {
		point.ProcessBytes = info.RSS
	}
	if list, err := process.Processes(); err == nil {
		point.ProcessCount = len(list)
	}
	counters, err := net.IOCounters(false)
	if err != nil || len(counters) == 0 {
		return point, errors.New("network metrics unavailable")
	}
	var total networkCounter
	for _, counter := range counters {
		total.received += counter.BytesRecv
		total.sent += counter.BytesSent
	}
	if !s.lastAt.IsZero() {
		elapsed := now.Sub(s.lastAt).Seconds()
		if elapsed > 0 {
			received := uint64(0)
			sent := uint64(0)
			if total.received >= s.previous.received {
				received = total.received - s.previous.received
			}
			if total.sent >= s.previous.sent {
				sent = total.sent - s.previous.sent
			}
			point.ReceiveBytesSec = uint64(float64(received) / elapsed)
			point.SendBytesSec = uint64(float64(sent) / elapsed)
		}
	}
	s.previous, s.lastAt = total, now
	return point, nil
}

func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; font-src 'self'; base-uri 'none'; frame-ancestors 'none'")
		c.Next()
	}
}
