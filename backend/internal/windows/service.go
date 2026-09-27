package windows

import (
	coreservice "desktopguardpro/internal/service"

	"golang.org/x/sys/windows/svc"
)

const ServiceName = "DesktopGuardPro"

type ServiceHandler struct {
	coordinator *coreservice.Coordinator
	powerEvent  func(uint32)
}

func (handler *ServiceHandler) SetPowerEventHandler(callback func(uint32)) {
	handler.powerEvent = callback
}

func NewServiceHandler(coordinator *coreservice.Coordinator) *ServiceHandler {
	return &ServiceHandler{coordinator: coordinator}
}

func (handler *ServiceHandler) Execute(
	_ []string,
	requests <-chan svc.ChangeRequest,
	statuses chan<- svc.Status,
) (bool, uint32) {
	statuses <- svc.Status{State: svc.StartPending}

	accepted := svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPowerEvent
	current := svc.Status{State: svc.Running, Accepts: accepted}
	statuses <- current

	for request := range requests {
		switch request.Cmd {
		case svc.Interrogate:
			statuses <- current
		case svc.PowerEvent:
			if handler.powerEvent != nil {
				handler.powerEvent(request.EventType)
			}
		case svc.Stop, svc.Shutdown:
			statuses <- svc.Status{State: svc.StopPending}
			return false, 0
		}
	}

	statuses <- svc.Status{State: svc.StopPending}
	return false, 0
}
