package main

//go:generate rsrc -manifest agent.manifest -arch amd64 -o rsrc_windows_amd64.syso

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"desktopguardpro/internal/agent"
)

func main() {
	release, acquired, err := agent.AcquireInteractiveAgentInstance()
	if err != nil {
		log.Fatalf("acquire Desktop Guard Pro agent instance: %v", err)
	}
	if !acquired {
		return
	}
	defer release()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := agent.NewRuntime().Run(ctx); err != nil {
		log.Fatalf("run Desktop Guard Pro agent: %v", err)
	}
}
