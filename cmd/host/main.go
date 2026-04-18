package main

import (
	"flag"
	"log"

	"github.com/relay-dev/relay/pkg/host"
)

func main() {
	serverAddr := flag.String("server", "localhost:8787", "Relay server address")
	password := flag.String("password", "", "Optional room password")
	recordPath := flag.String("record", "", "Record session to a JSONL file")
	flag.Parse()

	if err := host.Run(host.Config{
		ServerAddr: *serverAddr,
		Password:   *password,
		RecordPath: *recordPath,
	}); err != nil {
		log.Fatal(err)
	}
}
