// loadgen drives one URL with N keep-alive connections and prints throughput and latency.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"
)

func main() {
	url := flag.String("url", "", "target")
	body := flag.String("body", "", "POST body (GET if empty)")
	conns := flag.Int("c", 64, "concurrent connections")
	dur := flag.Duration("d", 8*time.Second, "duration")
	flag.Parse()

	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: *conns, MaxConnsPerHost: *conns}}
	var mu sync.Mutex
	var lat []time.Duration
	var errs int
	deadline := time.Now().Add(*dur)
	var wg sync.WaitGroup
	for i := 0; i < *conns; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var local []time.Duration
			var lerr int
			for time.Now().Before(deadline) {
				var req *http.Request
				if *body != "" {
					req, _ = http.NewRequest("POST", *url, bytes.NewReader([]byte(*body)))
					req.Header.Set("Content-Type", "application/json")
				} else {
					req, _ = http.NewRequest("GET", *url, nil)
				}
				t0 := time.Now()
				resp, err := client.Do(req)
				if err != nil {
					lerr++
					continue
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode != 200 {
					lerr++
					continue
				}
				local = append(local, time.Since(t0))
			}
			mu.Lock()
			lat = append(lat, local...)
			errs += lerr
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	p := func(q float64) time.Duration {
		if len(lat) == 0 {
			return 0
		}
		return lat[int(float64(len(lat)-1)*q)]
	}
	fmt.Printf("rps=%.0f p50=%.2fms p99=%.2fms errors=%d\n",
		float64(len(lat))/dur.Seconds(), ms(p(.5)), ms(p(.99)), errs)
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
