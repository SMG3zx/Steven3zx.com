package main

import (
	"context"
	"log"

	"golang.org/x/sys/windows/svc"
)

// runIfService runs run under the Windows service control manager when the
// process was started by it. It reports false when started from a console.
func runIfService(name string, run func(context.Context) error) (bool, error) {
	isService, err := svc.IsWindowsService()
	if err != nil || !isService {
		return false, err
	}
	return true, svc.Run(name, &serviceHandler{run: run})
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
			// The runner stopped on its own. Exit non-zero so the service
			// recovery actions set by install.ps1 restart it.
			if err != nil {
				log.Printf("runner stopped: %v", err)
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
					log.Printf("runner stopped: %v", err)
				}
				return false, 0
			}
		}
	}
}
