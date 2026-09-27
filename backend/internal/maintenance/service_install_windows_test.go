package maintenance

import (
	"errors"
	"testing"
	"time"

	winapi "golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc/mgr"
)

func TestConfigureServiceCreatesHardenedAutomaticService(t *testing.T) {
	service := &fakeManagedService{}
	manager := &fakeServiceManager{openErr: winapi.ERROR_SERVICE_DOES_NOT_EXIST, service: service}
	options := ValidatedInstallOptions{ServiceName: DefaultServiceName, ServiceExecutable: `C:\Program Files\Desktop Guard Pro\desktop-guard-service.exe`}

	created, err := configureService(manager, options)
	if err != nil {
		t.Fatalf("configureService() error = %v", err)
	}
	if !created {
		t.Fatal("configureService() created = false, want true")
	}
	if manager.createdName != options.ServiceName || manager.createdPath != options.ServiceExecutable {
		t.Fatalf("created service = %q %q", manager.createdName, manager.createdPath)
	}
	assertDesiredServiceConfig(t, manager.createdConfig, options.ServiceExecutable)
	assertRecoveryPolicy(t, service)
	if service.started || service.deleted {
		t.Fatalf("service state started=%t deleted=%t", service.started, service.deleted)
	}
}

func TestConfigureServiceUpdatesExistingService(t *testing.T) {
	service := &fakeManagedService{}
	manager := &fakeServiceManager{service: service}
	options := ValidatedInstallOptions{ServiceName: DefaultServiceName, ServiceExecutable: `C:\Program Files\Desktop Guard Pro\desktop-guard-service.exe`}

	created, err := configureService(manager, options)
	if err != nil {
		t.Fatalf("configureService() error = %v", err)
	}
	if created {
		t.Fatal("configureService() created = true, want false")
	}
	assertDesiredServiceConfig(t, service.updatedConfig, options.ServiceExecutable)
	assertRecoveryPolicy(t, service)
	if manager.createdName != "" || service.deleted {
		t.Fatalf("unexpected create or delete: name=%q deleted=%t", manager.createdName, service.deleted)
	}
}

func TestConfigureServiceRollsBackNewRegistrationAfterRecoveryFailure(t *testing.T) {
	recoveryErr := errors.New("recovery unavailable")
	service := &fakeManagedService{recoveryErr: recoveryErr}
	manager := &fakeServiceManager{openErr: winapi.ERROR_SERVICE_DOES_NOT_EXIST, service: service}

	_, err := configureService(manager, ValidatedInstallOptions{ServiceName: DefaultServiceName, ServiceExecutable: `C:\service.exe`})
	if !errors.Is(err, recoveryErr) {
		t.Fatalf("configureService() error = %v, want %v", err, recoveryErr)
	}
	if !service.deleted || service.started {
		t.Fatalf("service state started=%t deleted=%t", service.started, service.deleted)
	}
}

func TestDeleteServiceStopsBeforeDeletion(t *testing.T) {
	service := &fakeManagedService{}
	manager := &fakeServiceManager{service: service}
	if err := deleteService(manager, DefaultServiceName, 5*time.Second); err != nil {
		t.Fatalf("deleteService() error = %v", err)
	}
	if !service.stopped || !service.deleted {
		t.Fatalf("service stopped=%t deleted=%t", service.stopped, service.deleted)
	}
}

func TestDeleteServiceKeepsRegistrationWhenStopFails(t *testing.T) {
	wantErr := errors.New("stop timeout")
	service := &fakeManagedService{stopErr: wantErr}
	manager := &fakeServiceManager{service: service}
	err := deleteService(manager, DefaultServiceName, time.Second)
	if !errors.Is(err, wantErr) || service.deleted {
		t.Fatalf("deleteService() error=%v deleted=%t", err, service.deleted)
	}
}

func TestStopServicePropagatesStopFailure(t *testing.T) {
	wantErr := errors.New("stop timeout")
	service := &fakeManagedService{stopErr: wantErr}
	manager := &fakeServiceManager{service: service}
	err := stopService(manager, DefaultServiceName, time.Second)
	if !errors.Is(err, wantErr) || !service.stopped || service.deleted {
		t.Fatalf("stopService() error=%v stopped=%t deleted=%t", err, service.stopped, service.deleted)
	}
}

func assertDesiredServiceConfig(t *testing.T, config mgr.Config, executablePath string) {
	t.Helper()
	if config.BinaryPathName != winapi.EscapeArg(executablePath) || config.ServiceType != winapi.SERVICE_WIN32_OWN_PROCESS || config.StartType != mgr.StartAutomatic {
		t.Fatalf("service config = %#v", config)
	}
	if config.ServiceStartName != "LocalSystem" || config.SidType != winapi.SERVICE_SID_TYPE_UNRESTRICTED || config.Description == "" {
		t.Fatalf("service security config = %#v", config)
	}
}

func assertRecoveryPolicy(t *testing.T, service *fakeManagedService) {
	t.Helper()
	want := []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute, 0}
	if service.resetPeriod != serviceFailureResetPeriod || !service.recoverNonCrash || len(service.recoveryActions) != len(want) {
		t.Fatalf("recovery policy = %#v reset=%d nonCrash=%t", service.recoveryActions, service.resetPeriod, service.recoverNonCrash)
	}
	for index, delay := range want {
		if service.recoveryActions[index].Delay != delay {
			t.Fatalf("recovery action %d delay = %s, want %s", index, service.recoveryActions[index].Delay, delay)
		}
	}
	if service.recoveryActions[len(want)-1].Type != mgr.NoAction {
		t.Fatalf("final recovery action = %d, want no action", service.recoveryActions[len(want)-1].Type)
	}
}

type fakeServiceManager struct {
	service       *fakeManagedService
	openErr       error
	createdName   string
	createdPath   string
	createdConfig mgr.Config
}

func (manager *fakeServiceManager) OpenService(string) (managedService, error) {
	if manager.openErr != nil {
		return nil, manager.openErr
	}
	return manager.service, nil
}

func (manager *fakeServiceManager) CreateService(name, executablePath string, config mgr.Config) (managedService, error) {
	manager.createdName = name
	manager.createdPath = executablePath
	manager.createdConfig = config
	return manager.service, nil
}

func (manager *fakeServiceManager) Close() error { return nil }

type fakeManagedService struct {
	updatedConfig   mgr.Config
	recoveryActions []mgr.RecoveryAction
	resetPeriod     uint32
	recoverNonCrash bool
	recoveryErr     error
	startErr        error
	stopErr         error
	started         bool
	stopped         bool
	deleted         bool
}

func (service *fakeManagedService) UpdateConfig(config mgr.Config) error {
	service.updatedConfig = config
	return nil
}

func (service *fakeManagedService) SetRecoveryActions(actions []mgr.RecoveryAction, resetPeriod uint32) error {
	service.recoveryActions = append([]mgr.RecoveryAction(nil), actions...)
	service.resetPeriod = resetPeriod
	return service.recoveryErr
}

func (service *fakeManagedService) SetRecoveryActionsOnNonCrashFailures(enabled bool) error {
	service.recoverNonCrash = enabled
	return nil
}

func (service *fakeManagedService) Start() error {
	service.started = true
	return service.startErr
}

func (service *fakeManagedService) Stop(time.Duration) error {
	service.stopped = true
	return service.stopErr
}

func (service *fakeManagedService) Delete() error {
	service.deleted = true
	return nil
}

func (service *fakeManagedService) Close() error { return nil }
