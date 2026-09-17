package appbridge

import (
	"desktopguardpro/internal/contracts"
	"desktopguardpro/internal/domain"
	coreservice "desktopguardpro/internal/service"
)

func (bridge *Bridge) GetDirectoryMonitoring() (coreservice.DirectoryMonitoringResult, error) {
	var result coreservice.DirectoryMonitoringResult
	err := bridge.call(contracts.MessageTypeDirectoryMonitoringGet, struct{}{}, contracts.MessageTypeDirectoryMonitoringResult, &result)
	return result, err
}

func (bridge *Bridge) UpdateDirectoryMonitoring(directories []string) (coreservice.DirectoryMonitoringResult, error) {
	var result coreservice.DirectoryMonitoringResult
	err := bridge.call(
		contracts.MessageTypeDirectoryMonitoringUpdate,
		coreservice.DirectoryMonitoringResult{Directories: directories},
		contracts.MessageTypeDirectoryMonitoringResult,
		&result,
	)
	return result, err
}

func (bridge *Bridge) UpdateMonitoringTargets(targets []domain.MonitoringTarget) (coreservice.DirectoryMonitoringResult, error) {
	var result coreservice.DirectoryMonitoringResult
	err := bridge.call(
		contracts.MessageTypeDirectoryMonitoringUpdate,
		coreservice.DirectoryMonitoringResult{Targets: targets},
		contracts.MessageTypeDirectoryMonitoringResult,
		&result,
	)
	return result, err
}
