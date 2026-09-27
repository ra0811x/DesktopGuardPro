package main

import (
	"context"
	"log"

	coreservice "desktopguardpro/internal/service"
	platformwindows "desktopguardpro/internal/windows"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/debug"
)

func main() {
	isService, err := svc.IsWindowsService()
	if err != nil {
		log.Fatalf("detect Windows service context: %v", err)
	}
	dataDirectory, err := defaultDataDirectory()
	if err != nil {
		log.Fatalf("resolve service data directory: %v", err)
	}
	if isService {
		if err := platformwindows.SecureServiceDataDirectory(dataDirectory, platformwindows.ServiceName); err != nil {
			log.Fatalf("secure service data directory: %v", err)
		}
	}
	runtime, err := openApplicationRuntime(context.Background(), dataDirectory)
	if err != nil {
		log.Fatalf("initialize persistent runtime: %v", err)
	}
	defer runtime.Close()

	handler := platformwindows.NewServiceHandler(runtime.coordinator)
	handler.SetPowerEventHandler(func(eventType uint32) {
		if err := runtime.handlePowerEvent(eventType); err != nil {
			runtime.api.SetHealthStatus(coreservice.HealthStatusDegraded)
		}
	})

	listener, err := platformwindows.ListenControlPipe()
	if err != nil {
		log.Fatalf("listen on control pipe: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	collectorsDone := make(chan error, 1)
	retentionDone := make(chan error, 1)
	go func() {
		serverDone <- platformwindows.ServeControl(ctx, listener, runtime.api)
	}()
	go func() {
		collectorErr := runtime.collectors.Run(ctx)
		if collectorErr != nil && ctx.Err() == nil {
			runtime.api.SetHealthStatus(coreservice.HealthStatusDegraded)
		}
		collectorsDone <- collectorErr
	}()
	go func() {
		retentionErr := runtime.RunRetention(ctx)
		if retentionErr != nil && ctx.Err() == nil {
			runtime.api.SetHealthStatus(coreservice.HealthStatusDegraded)
		}
		retentionDone <- retentionErr
	}()

	if isService {
		err = svc.Run(platformwindows.ServiceName, handler)
	} else {
		err = debug.Run(platformwindows.ServiceName, handler)
	}
	cancel()
	_ = listener.Close()
	serverErr := <-serverDone
	collectorsErr := <-collectorsDone
	retentionErr := <-retentionDone
	if err != nil {
		log.Fatalf("run %s: %v", platformwindows.ServiceName, err)
	}
	if serverErr != nil {
		log.Fatalf("run control pipe: %v", serverErr)
	}
	if collectorsErr != nil {
		log.Fatalf("run system collectors: %v", collectorsErr)
	}
	if retentionErr != nil {
		log.Fatalf("run automatic session retention: %v", retentionErr)
	}
}
