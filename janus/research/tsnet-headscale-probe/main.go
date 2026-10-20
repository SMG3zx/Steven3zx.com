package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"tailscale.com/tsnet"
)

func main() {
	role := flag.String("role", "", "agent|ctrl")
	name := flag.String("name", "", "hostname")
	keyFile := flag.String("keyfile", "", "pre-auth key file")
	dir := flag.String("dir", "", "state dir")
	control := flag.String("control", "http://127.0.0.1:8080", "control url")
	peers := flag.String("peers", "", "comma separated hostnames to probe")
	hold := flag.Duration("hold", 150*time.Second, "how long to stay up")
	flag.Parse()

	key, _ := os.ReadFile(*keyFile)
	srv := &tsnet.Server{Dir: *dir, Hostname: *name, AuthKey: strings.TrimSpace(string(key)), ControlURL: *control, Logf: func(string, ...any) {}, UserLogf: func(string, ...any) {}}
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	st, err := srv.Up(ctx)
	cancel()
	if err != nil {
		log.Fatalf("[%s] UP FAILED: %v", *name, err)
	}
	var tags []string
	if st.Self != nil && st.Self.Tags != nil {
		tags = st.Self.Tags.AsSlice()
	}
	fmt.Printf("[%s] UP ips=%v tags=%v\n", *name, st.TailscaleIPs, tags)

	lc, err := srv.LocalClient()
	if err != nil {
		log.Fatal(err)
	}
	port := ":9000"
	if *role == "ctrl" {
		port = ":8000"
	}
	ln, err := srv.Listen("tcp", port)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		out := map[string]any{"served_by": *name, "path": r.URL.Path}
		who, werr := lc.WhoIs(r.Context(), r.RemoteAddr)
		if werr != nil {
			out["whois_error"] = werr.Error()
		} else {
			out["caller"] = who.Node.ComputedName
			out["caller_tags"] = who.Node.Tags
		}
		json.NewEncoder(w).Encode(out)
	})
	go http.Serve(ln, mux)

	client := srv.HTTPClient()
	client.Timeout = 5 * time.Second
	probe := func(target string) string {
		resp, err := client.Get("http://" + target)
		if err != nil {
			return "FAIL " + trim(err.Error())
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return "OK " + strings.TrimSpace(string(b))
	}
	end := time.Now().Add(*hold)
	for round := 0; time.Now().Before(end); round++ {
		time.Sleep(6 * time.Second)
		for _, p := range strings.Split(*peers, ",") {
			if p == "" {
				continue
			}
			fmt.Printf("[%s] %s -> %s : %s\n", *name, time.Now().Format("15:04:05"), p, probe(p))
		}
	}
}

func trim(s string) string {
	if len(s) > 110 {
		return s[:110]
	}
	return s
}
