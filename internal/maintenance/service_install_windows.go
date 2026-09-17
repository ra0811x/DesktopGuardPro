package maintenance

import (
	"errors"
	"fmt"
	"time"

	winapi "golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	serviceDisplayName        = "Desktop Guard Pro"
	serviceDescription        = "Desktop Guard Pro background monitoring and audit service."
	serviceFailureResetPeriod = 24 * 60 * 60
)

var ErrServiceUnavailable = errors.New("Windows service is unavailable")

type serviceManager interface {
	OpenService(name string) (managedService, error)
	CreateService(name, executablePath string, config mgr.Config) (managedService, error)
	Close() error
}

type managedService interface {
	UpdateConfig(config mgr.Config) error
	SetRecoveryActions(actions []mgr.RecoveryAction, resetPeriod uint32) error
	SetRecoveryActionsOnNonCrashFailures(enabled bool) error
	Start() error
	Stop(timeout time.Duration) error
	Delete() error
	Close() error
}

func ConfigureWindowsService(options ValidatedInstallOptions) (bool, error) {
	manager, err := connectServiceManager()
	if err != nil {
		return false, fmt.Errorf("connect to service control manager: %w", err)
	}
	defer manager.Close()
	return configureService(manager, options)
}

func StartWindowsService(serviceName string) error {
	manager, err := connectServiceManager()
	if err != nil {
		return fmt.Errorf("connect to service control manager: %w", err)
	}
	defer manager.Close()
	service, err := manager.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("open service %q: %w", serviceName, err)
	}
	defer service.Close()
	if err := service.Start(); err != nil && !errors.Is(err, winapi.ERROR_SERVICE_ALREADY_RUNNING) {
		return fmt.Errorf("start service %q: %w", serviceName, err)
	}
	return nil
}

func StopWindowsService(serviceName string) error {
	manager, err := connectServiceManager()
	if err != nil {
		return fmt.Errorf("connect to service control manager: %w", err)
	}
	defer manager.Close()
	return stopService(manager, serviceName, 30*time.Second)
}

func stopService(manager serviceManager, serviceName string, timeout time.Duration) error {
	service, err := manager.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("open service %q: %w", serviceName, err)
	}
	defer service.Close()
	if err := service.Stop(timeout); err != nil {
		return fmt.Errorf("stop service %q: %w", serviceName, err)
	}
	return nil
}

func DeleteWindowsService(serviceName string) error {
	manager, err := connectServiceManager()
	if err != nil {
		return fmt.Errorf("connect to service control manager: %w", err)
	}
	defer manager.Close()
	return deleteService(manager, serviceName, 30*time.Second)
}

func deleteService(manager serviceManager, serviceName string, stopTimeout time.Duration) error {
	service, err := manager.OpenService(serviceName)
	if errors.Is(err, winapi.ERROR_SERVICE_DOES_NOT_EXIST) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open service %q: %w", serviceName, err)
	}
	defer service.Close()
	if err := service.Stop(stopTimeout); err != nil {
		return fmt.Errorf("stop service %q: %w", serviceName, err)
	}
	if err := service.Delete(); err != nil {
		return fmt.Errorf("delete service %q: %w", serviceName, err)
	}
	return nil
}

func configureService(manager serviceManager, options ValidatedInstallOptions) (bool, error) {
	config := desiredServiceConfig(options.ServiceExecutable)
	service, err := manager.OpenService(options.ServiceName)
	created := false
	if errors.Is(err, winapi.ERROR_SERVICE_DOES_NOT_EXIST) {
		service, err = manager.CreateService(options.ServiceName, options.ServiceExecutable, config)
		created = err == nil
	} else if err == nil {
		err = service.UpdateConfig(config)
	}
	if err != nil {
		return false, fmt.Errorf("register service %q: %w", options.ServiceName, err)
	}
	defer service.Close()

	rollback := func(cause error) error {
		if created {
			if deleteErr := service.Delete(); deleteErr != nil {
				return errors.Join(cause, fmt.Errorf("roll back service registration: %w", deleteErr))
			}
		}
		return cause
	}
	if err := service.SetRecoveryActions(desiredRecoveryActions(), serviceFailureResetPeriod); err != nil {
		return false, rollback(fmt.Errorf("configure service recovery: %w", err))
	}
	if err := service.SetRecoveryActionsOnNonCrashFailures(true); err != nil {
		return false, rollback(fmt.Errorf("enable non-crash recovery: %w", err))
	}
	return created, nil
}

func desiredServiceConfig(executablePath string) mgr.Config {
	return mgr.Config{
		ServiceType:      winapi.SERVICE_WIN32_OWN_PROCESS,
		StartType:        mgr.StartAutomatic,
		ErrorControl:     mgr.ErrorNormal,
		BinaryPathName:   winapi.EscapeArg(executablePath),
		ServiceStartName: "LocalSystem",
		DisplayName:      serviceDisplayName,
		Description:      serviceDescription,
		SidType:          winapi.SERVICE_SID_TYPE_UNRESTRICTED,
	}
}

func desiredRecoveryActions() []mgr.RecoveryAction {
	return []mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 2 * time.Minute},
		{Type: mgr.NoAction},
	}
}

type windowsServiceManager struct {
	manager *mgr.Mgr
}

func connectServiceManager() (serviceManager, error) {
	manager, err := mgr.Connect()
	if err != nil {
		return nil, err
	}
	return &windowsServiceManager{manager: manager}, nil
}

func (manager *windowsServiceManager) OpenService(name string) (managedService, error) {
	service, err := manager.manager.OpenService(name)
	if err != nil {
		return nil, err
	}
	return &windowsManagedService{service: service}, nil
}

func (manager *windowsServiceManager) CreateService(name, executablePath string, config mgr.Config) (managedService, error) {
	service, err := manager.manager.CreateService(name, executablePath, config)
	if err != nil {
		return nil, err
	}
	return &windowsManagedService{service: service}, nil
}

func (manager *windowsServiceManager) Close() error {
	return manager.manager.Disconnect()
}

type windowsManagedService struct {
	service *mgr.Service
}

func (service *windowsManagedService) UpdateConfig(config mgr.Config) error {
	return service.service.UpdateConfig(config)
}

func (service *windowsManagedService) SetRecoveryActions(actions []mgr.RecoveryAction, resetPeriod uint32) error {
	return service.service.SetRecoveryActions(actions, resetPeriod)
}

func (service *windowsManagedService) SetRecoveryActionsOnNonCrashFailures(enabled bool) error {
	return service.service.SetRecoveryActionsOnNonCrashFailures(enabled)
}

func (service *windowsManagedService) Start() error {
	return service.service.Start()
}

func (service *windowsManagedService) Stop(timeout time.Duration) error {
	status, err := service.service.Query()
	if err != nil {
		return err
	}
	if status.State == svc.Stopped {
		return nil
	}
	if status.State != svc.StopPending {
		if _, err := service.service.Control(svc.Stop); err != nil {
			return err
		}
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		status, err = service.service.Query()
		if err != nil {
			return err
		}
		if status.State == svc.Stopped {
			return nil
		}
	}
	return fmt.Errorf("service stop timed out after %s", timeout)
}

func (service *windowsManagedService) Delete() error {
	return service.service.Delete()
}

func (service *windowsManagedService) Close() error {
	return service.service.Close()
}
