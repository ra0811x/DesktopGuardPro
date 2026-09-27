package collector

import (
	"encoding/binary"
	"errors"
	"testing"

	"golang.org/x/sys/windows/registry"
)

func TestStartupInventoryIncludesCommandScopeAndUser(t *testing.T) {
	command := []uint16{'C', ':', '\\', 'a', '.', 'e', 'x', 'e', 0}
	data := make([]byte, len(command)*2)
	for index, value := range command {
		binary.LittleEndian.PutUint16(data[index*2:], value)
	}
	target := RegistryTarget{Name: "user_startup_S-1-5-21-100_run", Path: `S-1-5-21-100\Software\Microsoft\Windows\CurrentVersion\Run`}
	items := startupInventoryFromValues(target, map[string]RegistryValue{"item": {KeyPath: target.Path, ValueName: "Example", Data: data}})
	if len(items) != 1 || items[0].Attributes["command"] != `C:\a.exe` || items[0].Attributes["scope"] != "user" ||
		items[0].Attributes["userSID"] != "S-1-5-21-100" {
		t.Fatalf("startup items = %#v", items)
	}
}

func TestCollectSupplementalSoftwareInventorySkipsMissingOptionalSource(t *testing.T) {
	assets, err := collectSupplementalSoftwareInventory([]supplementalInventorySource{
		{
			name: "AppX", optional: true,
			snapshot: func() ([]InstalledSoftware, error) {
				return nil, registry.ErrNotExist
			},
		},
		{
			name: "services",
			snapshot: func() ([]InstalledSoftware, error) {
				return []InstalledSoftware{{Identifier: `service\audit`, Name: "Audit Service"}}, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("collectSupplementalSoftwareInventory() error = %v", err)
	}
	if len(assets) != 1 || assets[0].Identifier != `service\audit` {
		t.Fatalf("assets = %#v, want remaining source data", assets)
	}
}

func TestCollectSupplementalSoftwareInventoryReportsRequiredSourceFailures(t *testing.T) {
	failure := errors.New("access denied")
	_, err := collectSupplementalSoftwareInventory([]supplementalInventorySource{{
		name: "services",
		snapshot: func() ([]InstalledSoftware, error) {
			return nil, failure
		},
	}})
	if !errors.Is(err, failure) {
		t.Fatalf("collectSupplementalSoftwareInventory() error = %v, want required source failure", err)
	}
}

func TestAppXPackageInventoryIncludesRequiredSoftwareFields(t *testing.T) {
	software := appXPackageInventory(
		"Contoso.Editor_2.1.3.0_x64_neutral_abcd1234", "Contoso Editor", "CN=Contoso", `C:\Program Files\WindowsApps\Contoso.Editor`,
	)
	if software.Name != "Contoso Editor" || software.Version != "2.1.3.0" || software.Publisher != "CN=Contoso" ||
		software.Attributes["installScope"] != "machine" || software.Attributes["installLocation"] == "" {
		t.Fatalf("AppX inventory = %#v", software)
	}
}

func TestInstalledSoftwareTargetsIncludeMachineAndLoadedUsers(t *testing.T) {
	targets := installedSoftwareTargets([]string{".DEFAULT", "S-1-5-21-100", "S-1-5-21-100_Classes"})
	if len(targets) != 4 || targets[0].scope != "machine" || targets[2].scope != "user" ||
		targets[2].userSID != "S-1-5-21-100" || targets[3].view != registry.WOW64_32KEY {
		t.Fatalf("software targets = %#v", targets)
	}
}
