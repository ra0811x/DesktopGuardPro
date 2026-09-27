package installpolicy

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAndLoadPolicy(t *testing.T) {
	directory := t.TempDir()
	policy, err := New("S-1-5-21-1000-1001-1002-1003")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := Save(directory, policy); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := Load(directory)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded != policy {
		t.Fatalf("Load() = %#v, want %#v", loaded, policy)
	}
}

func TestSaveReplacesExistingPolicy(t *testing.T) {
	directory := t.TempDir()
	first, _ := New("S-1-5-21-1000-1001-1002-1003")
	second, _ := New("S-1-5-21-2000-2001-2002-2003")
	if err := Save(directory, first); err != nil {
		t.Fatal(err)
	}
	if err := Save(directory, second); err != nil {
		t.Fatalf("replace policy: %v", err)
	}
	loaded, err := Load(directory)
	if err != nil || loaded != second {
		t.Fatalf("Load() = %#v, %v; want %#v", loaded, err, second)
	}
}

func TestEnsureOwnerKeepsMatchingPolicyAndRejectsTakeover(t *testing.T) {
	directory := t.TempDir()
	owner, _ := New("S-1-5-21-1000-1001-1002-1003")
	other, _ := New("S-1-5-21-2000-2001-2002-2003")
	if err := EnsureOwner(directory, owner); err != nil {
		t.Fatalf("create owner policy: %v", err)
	}
	if err := EnsureOwner(directory, owner); err != nil {
		t.Fatalf("match owner policy: %v", err)
	}
	if err := EnsureOwner(directory, other); !errors.Is(err, ErrPolicyOwnerMismatch) {
		t.Fatalf("EnsureOwner() error = %v, want %v", err, ErrPolicyOwnerMismatch)
	}
	loaded, err := Load(directory)
	if err != nil || loaded != owner {
		t.Fatalf("Load() = %#v, %v; want original owner %#v", loaded, err, owner)
	}
}

func TestLoadRejectsMalformedPolicies(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "unknown field", content: `{"version":1,"ownerUserSid":"S-1-5-21-1","extra":true}`},
		{name: "trailing object", content: `{"version":1,"ownerUserSid":"S-1-5-21-1"}{}`},
		{name: "unsupported version", content: `{"version":2,"ownerUserSid":"S-1-5-21-1"}`},
		{name: "invalid SID", content: `{"version":1,"ownerUserSid":"owner"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			if err := os.WriteFile(filepath.Join(directory, FileName), []byte(test.content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(directory)
			if !errors.Is(err, ErrPolicyInvalid) {
				t.Fatalf("Load() error = %v, want %v", err, ErrPolicyInvalid)
			}
		})
	}
}

func TestLoadRejectsOversizedPolicy(t *testing.T) {
	directory := t.TempDir()
	content := make([]byte, maximumPolicySize+1)
	if err := os.WriteFile(filepath.Join(directory, FileName), content, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(directory)
	if !errors.Is(err, ErrPolicyInvalid) {
		t.Fatalf("Load() error = %v, want %v", err, ErrPolicyInvalid)
	}
}
