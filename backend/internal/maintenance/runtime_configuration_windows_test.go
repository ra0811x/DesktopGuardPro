package maintenance

import (
	"errors"
	"testing"
)

func TestUnsupportedRuntimeConfigurationFailsBeforeMaintenance(t *testing.T) {
	for name, call := range map[string]func() error{
		"install": func() error {
			_, err := InstallWindows(InstallOptions{DataDirectory: "D:\\CustomData", ServiceName: "CustomService"})
			return err
		},
		"deploy": func() error {
			_, err := DeployWindows(DeployOptions{DataDirectory: "D:\\CustomData", ServiceName: "CustomService"})
			return err
		},
		"upgrade": func() error {
			_, err := UpgradeWindows(UpgradeOptions{DataDirectory: "D:\\CustomData", ServiceName: "CustomService"})
			return err
		},
		"uninstall": func() error {
			_, err := UninstallWindows(UninstallOptions{DataDirectory: "D:\\CustomData", ServiceName: "CustomService"})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, ErrUnsupportedRuntimeConfiguration) {
				t.Fatalf("unsupported settings reached maintenance: %v", err)
			}
		})
	}
}

func TestFixedRuntimeConfigurationMatchesServiceEntryPoint(t *testing.T) {
	expected, err := expectedServiceDataDirectory()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", DefaultServiceName} {
		if err := validateRuntimeConfiguration(expected, name); err != nil {
			t.Fatal(err)
		}
	}
}
