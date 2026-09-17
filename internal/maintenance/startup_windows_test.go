package maintenance

import (
	"errors"
	"testing"
)

func TestRegisterAgentStartupUsesMachineRunEntryAndEscapedCommand(t *testing.T) {
	store := &fakeStartupValueStore{}
	agentExecutable := `C:\Program Files\Desktop Guard Pro\desktop-guard-agent.exe`

	if err := registerAgentStartup(store, agentExecutable); err != nil {
		t.Fatalf("registerAgentStartup() error = %v", err)
	}
	if store.name != startupValueName {
		t.Fatalf("startup value name = %q, want %q", store.name, startupValueName)
	}
	want := `"C:\Program Files\Desktop Guard Pro\desktop-guard-agent.exe" --autostart`
	if store.value != want {
		t.Fatalf("startup command = %q, want %q", store.value, want)
	}
}

func TestRegisterAgentStartupReturnsRegistryFailure(t *testing.T) {
	wantErr := errors.New("access denied")
	store := &fakeStartupValueStore{setErr: wantErr}

	err := registerAgentStartup(store, `C:\DesktopGuardPro\agent.exe`)
	if !errors.Is(err, wantErr) {
		t.Fatalf("registerAgentStartup() error = %v, want %v", err, wantErr)
	}
}

type fakeStartupValueStore struct {
	name   string
	value  string
	setErr error
}

func (store *fakeStartupValueStore) SetStringValue(name, value string) error {
	store.name = name
	store.value = value
	return store.setErr
}

func (*fakeStartupValueStore) DeleteValue(string) error { return nil }

func (*fakeStartupValueStore) Close() error { return nil }
