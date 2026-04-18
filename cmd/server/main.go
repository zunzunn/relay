package main

import (
	"flag"
	"log"

	"github.com/relay-dev/relay/pkg/server"
)

func main() {
	port := flag.Int("port", 8787, "Port to listen on")
	flag.Parse()

	log.SetFlags(log.LstdFlags)
	server.Run(*port)
}
