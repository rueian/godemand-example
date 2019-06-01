package main

import (
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/rueian/godemand-example/pgproxy"
)

func main() {
	godemandHost := os.Getenv("GODEMAND_ADDR")
	if godemandHost == "" {
		godemandHost = "http://godemand"
	}

	resolver := &pgproxy.GodemandResolver{
		Host:        godemandHost,
		DatabaseMap: pgproxy.DatabaseMap,
	}

	broker := pgproxy.NewPGBroker(resolver)

	ln, err := net.Listen("tcp", ":5432")
	if err != nil {
		log.Fatal(err)
	}

	go broker.Serve(ln)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs
	broker.Shutdown()
}
