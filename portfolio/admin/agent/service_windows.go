package main

import (
	"context"
	"log"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/svc"
)

// serviceName is the Windows service that install.ps1 registers.
const serviceName = "steven3zx-host-agent"

// runIfService runs run under the Windows service control manager when the
// process was started by it. It reports false when started from a console.
// Under the service, logs go to hostagent.log beside the executable, since a
// service has no console to write to.
func runIfService(run func(context.Context) error) (bool, error) {
	isService, err := svc.IsWindowsService()
	if err != nil || !isService {
		return false, err
	}
	exe, err := os.Executable()
	if err != nil {
		return true, err
	}
	logFile, err := os.OpenFile(filepath.Join(filepath.Dir(exe), "hostagent.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return true, err
	}
	defer logFile.Close()
	log.SetOutput(logFile)
	return true, svc.Run(serviceName, &serviceHandler{run: run})
}

type serviceHandler struct {
	run func(context.Context) error
}

func (h *serviceHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.run(ctx) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	for {
		select {
		case err := <-done:
			// The supervisor stopped on its own. Exit non-zero so the service
			// recovery actions in install.ps1 restart it.
			if err != nil {
				log.Printf("supervisor stopped: %v", err)
				return false, 1
			}
			return false, 0
		case req := <-requests:
			switch req.Cmd {
			case svc.Interrogate:
				status <- req.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				if err := <-done; err != nil {
					log.Printf("supervisor stopped: %v", err)
				}
				return false, 0
			}
		}
	}
}
