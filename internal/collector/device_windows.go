package collector

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"golang.org/x/sys/windows"
)

func snapshotWindowsDevices() ([]DeviceInfo, error) {
	deviceSet, err := windows.SetupDiGetClassDevsEx(
		nil,
		"",
		0,
		windows.DIGCF_PRESENT|windows.DIGCF_ALLCLASSES,
		0,
		"",
	)
	if err != nil {
		return nil, fmt.Errorf("create present-device set: %w", err)
	}
	defer deviceSet.Close()

	devices := make([]DeviceInfo, 0, 64)
	for index := 0; ; index++ {
		data, err := deviceSet.EnumDeviceInfo(index)
		if errors.Is(err, windows.ERROR_NO_MORE_ITEMS) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("enumerate device %d: %w", index, err)
		}
		instanceID, err := windows.SetupDiGetDeviceInstanceId(deviceSet, data)
		if err != nil {
			continue
		}
		class := deviceStringProperty(deviceSet, data, windows.SPDRP_CLASS)
		if !auditedDevice(instanceID, class) {
			continue
		}
		name := deviceStringProperty(deviceSet, data, windows.SPDRP_FRIENDLYNAME)
		if name == "" {
			name = deviceStringProperty(deviceSet, data, windows.SPDRP_DEVICEDESC)
		}
		devices = append(devices, DeviceInfo{
			InstanceID:   instanceID,
			Class:        class,
			Name:         name,
			Manufacturer: deviceStringProperty(deviceSet, data, windows.SPDRP_MFG),
		})
	}
	if storage, err := snapshotUSBStorageDetails(); err == nil {
		devices = enrichUSBStorageDevices(devices, storage)
	}
	return devices, nil
}

type usbStorageDetails struct {
	InstanceID   string   `json:"InstanceId"`
	Model        string   `json:"Model"`
	SerialNumber string   `json:"SerialNumber"`
	VolumeLabels []string `json:"VolumeLabels"`
	DriveLetters []string `json:"DriveLetters"`
}

func snapshotUSBStorageDetails() ([]usbStorageDetails, error) {
	script := `$items = @(Get-CimInstance Win32_DiskDrive | Where-Object InterfaceType -eq 'USB' | ForEach-Object { $disk = $_; $logical = @(Get-CimAssociatedInstance -InputObject $disk -Association Win32_DiskDriveToDiskPartition | ForEach-Object { Get-CimAssociatedInstance -InputObject $_ -Association Win32_LogicalDiskToPartition }); [pscustomobject]@{ InstanceId = $disk.PNPDeviceID; Model = $disk.Model; SerialNumber = $disk.SerialNumber; VolumeLabels = @($logical | ForEach-Object { $_.VolumeName }); DriveLetters = @($logical | ForEach-Object { $_.DeviceID }) } }); ConvertTo-Json -InputObject $items -Compress`
	output, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "$ErrorActionPreference='Stop'; "+script).Output()
	if err != nil {
		return nil, fmt.Errorf("read USB storage details: %w", err)
	}
	return parseUSBStorageDetails(output)
}

func parseUSBStorageDetails(output []byte) ([]usbStorageDetails, error) {
	if strings.TrimSpace(string(output)) == "" {
		return nil, nil
	}
	var details []usbStorageDetails
	if err := json.Unmarshal(output, &details); err != nil {
		return nil, fmt.Errorf("decode USB storage details: %w", err)
	}
	return details, nil
}

func enrichUSBStorageDevices(devices []DeviceInfo, details []usbStorageDetails) []DeviceInfo {
	byID := make(map[string]int, len(devices))
	for index := range devices {
		byID[strings.ToLower(devices[index].InstanceID)] = index
	}
	for _, detail := range details {
		instanceID := strings.TrimSpace(detail.InstanceID)
		if instanceID == "" {
			continue
		}
		index, exists := byID[strings.ToLower(instanceID)]
		if !exists {
			devices = append(devices, DeviceInfo{InstanceID: instanceID, Class: "DiskDrive", Name: strings.TrimSpace(detail.Model)})
			index = len(devices) - 1
			byID[strings.ToLower(instanceID)] = index
		}
		labels, letters := cleanSortedValues(detail.VolumeLabels), cleanSortedValues(detail.DriveLetters)
		devices[index].Model = strings.TrimSpace(detail.Model)
		devices[index].SerialNumber = strings.TrimSpace(detail.SerialNumber)
		devices[index].VolumeLabels = strings.Join(labels, ",")
		devices[index].DriveLetters = strings.Join(letters, ",")
	}
	return devices
}

func cleanSortedValues(values []string) []string {
	cleaned := values[:0]
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			cleaned = append(cleaned, value)
		}
	}
	sort.Strings(cleaned)
	return cleaned
}

func deviceStringProperty(deviceSet windows.DevInfo, data *windows.DevInfoData, property windows.SPDRP) string {
	value, err := windows.SetupDiGetDeviceRegistryProperty(deviceSet, data, property)
	if err != nil {
		return ""
	}
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func auditedDevice(instanceID, _ string) bool {
	upperID := strings.ToUpper(instanceID)
	for _, prefix := range []string{"USB\\", "USBSTOR\\", "HID\\", "SWD\\WPDBUSENUM\\", "BTH\\", "BTHENUM\\"} {
		if strings.HasPrefix(upperID, prefix) {
			return true
		}
	}
	return false
}
