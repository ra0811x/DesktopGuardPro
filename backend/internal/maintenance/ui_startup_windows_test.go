package maintenance

import (
	"errors"
	"testing"

	"desktopguardpro/internal/installpolicy"
	"golang.org/x/sys/windows/registry"
)

func TestUIStartupRemovalOnlyDeletesMatchingInstallation(t *testing.T) {
	uiPath := `C:\Program Files\Desktop Guard Pro\desktop-guard-ui.exe`
	for _, test := range []struct {
		name, value string
		readErr     error
		wantDelete  bool
	}{
		{name: "current installation", value: `"` + uiPath + `" --autostart`, wantDelete: true},
		{name: "case insensitive path", value: `"c:\program files\desktop guard pro\desktop-guard-ui.exe" --autostart`, wantDelete: true},
		{name: "another installation", value: `"D:\Other\desktop-guard-ui.exe" --autostart`},
		{name: "another program", value: `"C:\Program Files\Other\app.exe"`},
		{name: "absent", readErr: registry.ErrNotExist},
		{name: "different value type", readErr: registry.ErrUnexpectedType},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeUIStartupStore{value: test.value, readErr: test.readErr}
			if err := unregisterUIStartup(store, uiPath); err != nil {
				t.Fatal(err)
			}
			if (store.deleted == uiStartupValueName) != test.wantDelete {
				t.Fatalf("deleted value = %q, wantDelete = %t", store.deleted, test.wantDelete)
			}
		})
	}
}

func TestUIStartupRemovalRejectsRegistryFailuresAndInvalidOwner(t *testing.T) {
	denied := errors.New("denied")
	uiPath := `C:\Program Files\Desktop Guard Pro\desktop-guard-ui.exe`
	for _, store := range []*fakeUIStartupStore{
		{readErr: denied},
		{value: `"` + uiPath + `" --autostart`, deleteErr: denied},
	} {
		if err := unregisterUIStartup(store, uiPath); !errors.Is(err, denied) {
			t.Fatalf("unregister error = %v", err)
		}
	}
	if err := UnregisterUIStartup(`not-a-sid\Software`, uiPath); err == nil {
		t.Fatal("invalid owner must be rejected before opening any registry key")
	}
}

func TestUninstallRemovesVerifiedOwnerUIStartupBeforeService(t *testing.T) {
	for _, managed := range []bool{false, true} {
		var calls []string
		dependencies := successfulUninstallerDependencies(&calls)
		dependencies.unregisterUIStartup = func(ownerSID, path string) error {
			if ownerSID != uninstallOwnerSID || path != `C:\Program Files\Desktop Guard Pro\desktop-guard-ui.exe` {
				t.Fatalf("untrusted UI startup target: %q %q", ownerSID, path)
			}
			calls = append(calls, "unregister-ui-startup")
			return nil
		}
		if _, err := uninstallWindows(UninstallOptions{ManagedByMSI: managed}, dependencies); err != nil {
			t.Fatal(err)
		}
		if indexOfCall(calls, "unregister-ui-startup") >= indexOfCall(calls, "delete-service") {
			t.Fatalf("UI cleanup happened after deleting service: %v", calls)
		}
	}
}

func TestUninstallUIStartupCleanupRetainsOwnerAndSessionGates(t *testing.T) {
	for _, gate := range []string{"owner", "session", "registry"} {
		t.Run(gate, func(t *testing.T) {
			var calls []string
			dependencies := successfulUninstallerDependencies(&calls)
			denied := errors.New("UI registry denied")
			dependencies.unregisterUIStartup = func(string, string) error {
				calls = append(calls, "unregister-ui-startup")
				return denied
			}
			if gate == "owner" {
				dependencies.currentPolicy = func() (installpolicy.Policy, error) {
					return installpolicy.New("S-1-5-21-2000-2001-2002-2003")
				}
			} else if gate == "session" {
				dependencies.checkNoActiveSession = func() error { return ErrActiveProtectionSession }
			}
			result, err := uninstallWindows(UninstallOptions{}, dependencies)
			if err == nil || countCalls(calls, "delete-service") != 0 {
				t.Fatalf("cleanup failure must stop before service deletion: %v %v", calls, err)
			}
			if gate != "registry" && countCalls(calls, "unregister-ui-startup") != 0 {
				t.Fatal("cleanup bypassed owner/session gate")
			}
			if gate == "registry" && (!errors.Is(err, denied) || result.Stage != "remove_ui_startup") {
				t.Fatalf("failure lost startup diagnostic: %+v %v", result, err)
			}
		})
	}
}

type fakeUIStartupStore struct {
	value, deleted     string
	readErr, deleteErr error
}

func (store *fakeUIStartupStore) GetStringValue(name string) (string, uint32, error) {
	if name != uiStartupValueName {
		return "", 0, errors.New("unexpected startup value")
	}
	return store.value, registry.SZ, store.readErr
}

func (store *fakeUIStartupStore) DeleteValue(name string) error {
	store.deleted = name
	return store.deleteErr
}

func indexOfCall(calls []string, wanted string) int {
	for index, call := range calls {
		if call == wanted {
			return index
		}
	}
	return -1
}
