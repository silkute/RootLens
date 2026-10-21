package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"rootlens/internal/agent"
	"runtime"
	"syscall"
)

func main() {
	config := flag.String("config", "agent.json", "Agent configuration file")
	flag.Parse()
	if runtime.GOOS != "linux" {
		log.Print("The monitoring agent requires Linux; use the hub demo for other platforms.")
		os.Exit(1)
	}
	c, e := agent.LoadConfig(*config)
	if e != nil {
		log.Print(e)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if e = agent.Run(ctx, c); e != nil {
		log.Print(e)
		os.Exit(1)
	}
}
