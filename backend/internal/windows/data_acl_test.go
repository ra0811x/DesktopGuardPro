package windows

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	winapi "golang.org/x/sys/windows"
)

func TestServiceDataDirectoryRejectsPrecreatedUntrustedOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "precreated")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := winapi.SetNamedSecurityInfo(path, winapi.SE_FILE_OBJECT, winapi.OWNER_SECURITY_INFORMATION, currentTokenUserSID(t), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	descriptor, err := winapi.GetNamedSecurityInfo(path, winapi.SE_FILE_OBJECT, winapi.OWNER_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		t.Fatal(err)
	}
	if owner.IsWellKnown(winapi.WinLocalSystemSid) || owner.IsWellKnown(winapi.WinBuiltinAdministratorsSid) {
		t.Skip("test needs a user-owned directory")
	}
	if err := SecureServiceDataDirectory(path, "UnregisteredTestService"); !errors.Is(err, ErrDataDirectoryInvalid) {
		t.Fatalf("precreated untrusted owner was not rejected: %v", err)
	}
}

func TestSecureDataDirectoryAppliesProtectedInheritedACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data")
	currentUserSID := currentTokenUserSID(t)
	if err := secureDataDirectory(path, currentUserSID); err != nil {
		t.Fatal(err)
	}
	descriptor, err := winapi.GetNamedSecurityInfo(path, winapi.SE_FILE_OBJECT,
		winapi.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&winapi.SE_DACL_PROTECTED == 0 {
		t.Fatal("service data directory DACL inherits from its parent")
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if dacl == nil || dacl.AceCount < 3 {
		t.Fatalf("service data directory ACE count = %v, want at least 3", dacl)
	}
	descriptorText := descriptor.String()
	for _, requiredPrincipal := range []string{";;;SY)", ";;;BA)", currentUserSID.String()} {
		if !strings.Contains(descriptorText, requiredPrincipal) {
			t.Fatalf("service data directory ACL is missing %s: %s", requiredPrincipal, descriptorText)
		}
	}
	if !strings.Contains(descriptorText, ";;;OW)") {
		t.Fatalf("implicit owner rights were not constrained: %s", descriptorText)
	}
	child := filepath.Join(path, "child.txt")
	if err := os.WriteFile(child, []byte("test"), 0o600); err != nil {
		t.Fatalf("current service principal cannot create child: %v", err)
	}
	childDescriptor, err := winapi.GetNamedSecurityInfo(child, winapi.SE_FILE_OBJECT,
		winapi.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(childDescriptor.String(), currentUserSID.String()) {
		t.Fatal("child file did not inherit the service principal ACE")
	}
}

func TestSecureDataDirectoryRejectsReparseTarget(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	if err := secureDataDirectory(link, currentTokenUserSID(t)); err != ErrDataDirectoryInvalid {
		t.Fatalf("expected invalid directory error, got %v", err)
	}
}

func TestSecureOwnerDirectoryGrantsInstalledOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner-logs")
	ownerSID := currentTokenUserSID(t).String()
	if err := SecureOwnerDirectory(path, ownerSID); err != nil {
		t.Fatalf("SecureOwnerDirectory() error = %v", err)
	}
	descriptor, err := winapi.GetNamedSecurityInfo(path, winapi.SE_FILE_OBJECT, winapi.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(descriptor.String(), ownerSID) {
		t.Fatalf("owner directory ACL is missing %s: %s", ownerSID, descriptor.String())
	}
}

func TestSecureOwnerDirectoryRejectsInvalidSID(t *testing.T) {
	if err := SecureOwnerDirectory(filepath.Join(t.TempDir(), "logs"), "owner"); err != ErrDataDirectoryInvalid {
		t.Fatalf("SecureOwnerDirectory() error = %v, want %v", err, ErrDataDirectoryInvalid)
	}
}

func TestMaintenanceDirectoryTrustRejectsUserWritableData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trusted-data")
	if err := SecureServiceDataDirectory(path, "EventLog"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMaintenanceDataDirectory(path); err != nil {
		t.Fatal(err)
	}
	acl, err := winapi.ACLFromEntries([]winapi.EXPLICIT_ACCESS{fullDirectoryAccess(currentTokenUserSID(t))}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := winapi.SetNamedSecurityInfo(path, winapi.SE_FILE_OBJECT, winapi.DACL_SECURITY_INFORMATION|winapi.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMaintenanceDataDirectory(path); !errors.Is(err, ErrDataDirectoryInvalid) {
		t.Fatalf("user writable maintenance state accepted: %v", err)
	}
}

func currentTokenUserSID(t *testing.T) *winapi.SID {
	t.Helper()
	token, err := winapi.OpenCurrentProcessToken()
	if err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	return user.User.Sid
}
