// hub runs a NATS server with a trivial echo responder, shared by both gateways.
package main

import (
	"log"
	"os"
	"os/signal"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

func main() {
	s, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: 4333, NoLog: true, NoSigs: true})
	if err != nil {
		log.Fatal(err)
	}
	go s.Start()
	if !s.ReadyForConnections(5e9) {
		log.Fatal("nats not ready")
	}
	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		log.Fatal(err)
	}
	if _, err := nc.Subscribe("bench.echo", func(m *nats.Msg) { m.Respond([]byte(`{"echo":true,"n":1}`)) }); err != nil {
		log.Fatal(err)
	}
	log.Printf("hub on %s", s.ClientURL())
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c
}
