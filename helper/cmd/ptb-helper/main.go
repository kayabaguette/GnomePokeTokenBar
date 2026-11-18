// Command ptb-helper is the PokeTokenBar-GNOME backend process.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"

	"poketokenbar/helper/internal/ipc"
)

// version is set via -ldflags "-X main.version=...".
var version = "dev"

const protocol = 1

func main() {
	log.SetFlags(0)
	log.SetPrefix("ptb-helper: ")
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: ptb-helper --version | serve | snapshot --json")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "--version":
		fmt.Println(version)
	case "serve":
		os.Exit(serve(os.Stdin, os.Stdout))
	case "snapshot":
		// Placeholder until the usage pipeline lands.
		fmt.Println(`{"type":"state","state":{}}`)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", os.Args[1])
		os.Exit(2)
	}
}

type request struct {
	ID  any    `json:"id"`
	Cmd string `json:"cmd"`
}

func serve(in io.Reader, out io.Writer) int {
	w := ipc.NewWriter(out)
	if err := w.Write(map[string]any{"type": "hello", "protocol": protocol, "version": version}); err != nil {
		log.Printf("write hello: %v", err)
		return 1
	}
	r := ipc.NewReader(in)
	for {
		line, err := r.Next()
		if err == io.EOF {
			return 0
		}
		if err != nil {
			log.Printf("read: %v", err)
			return 1
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			log.Printf("malformed line: %v", err)
			continue
		}
		var res map[string]any
		switch req.Cmd {
		case "ping":
			res = map[string]any{"type": "result", "id": req.ID, "ok": true}
		default:
			res = map[string]any{"type": "result", "id": req.ID, "ok": false, "error": "unknown command"}
		}
		if err := w.Write(res); err != nil {
			log.Printf("write: %v", err)
			return 1
		}
	}
}
