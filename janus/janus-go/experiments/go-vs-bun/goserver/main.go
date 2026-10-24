// goserver is the Go side of the comparison: net/http plus nats.go.
package main

import (
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/nats-io/nats.go"
)

type validateBody struct {
	Name string   `json:"name"`
	Age  int      `json:"age"`
	Tags []string `json:"tags"`
}

func fib(n int) int {
	if n < 2 {
		return n
	}
	return fib(n-1) + fib(n-2)
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8081", "listen address")
	natsURL := flag.String("nats", "nats://127.0.0.1:4333", "NATS url")
	flag.Parse()

	nc, err := nats.Connect(*natsURL)
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	})
	mux.HandleFunc("POST /validate", func(w http.ResponseWriter, r *http.Request) {
		var b validateBody
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil || b.Name == "" || b.Age < 0 || b.Age > 150 || len(b.Tags) > 20 {
			http.Error(w, `{"error":"invalid"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"name": b.Name, "age": b.Age, "tags": len(b.Tags)})
	})
	mux.HandleFunc("GET /nats", func(w http.ResponseWriter, r *http.Request) {
		msg, err := nc.Request("bench.echo", []byte(`{"n":1}`), 2*time.Second)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(msg.Data)
	})
	mux.HandleFunc("GET /cpu", func(w http.ResponseWriter, r *http.Request) {
		sum := sha256.Sum256([]byte("x"))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"fib":%d,"h":%d}`, fib(27), sum[0])
	})
	log.Printf("go listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
