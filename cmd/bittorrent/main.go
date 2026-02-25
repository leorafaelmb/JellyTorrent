package main

import (
	"flag"
	"os"

	"github.com/leorafaelmb/BitTorrent-Client/internal/logger"
)

func main() {
	debug := flag.Bool("debug", true, "enable debug logging")
	flag.Parse()

	if os.Getenv("JELLYTORRENT_DEBUG") == "1" || *debug {
		logger.Init(true)
	}

	args := flag.Args()
	if len(args) < 1 {
		logger.Log.Error("not enough arguments")
		os.Exit(1)
	}
	if err := runCommand(args[0], args); err != nil {
		logger.Log.Error("command failed", "error", err)
		os.Exit(1)
	}
}
