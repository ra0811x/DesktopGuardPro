package windows

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	winapi "golang.org/x/sys/windows"
)

var (
	ErrDataDirectoryInvalid  = errors.New("service data directory is invalid")
	ErrServiceSIDUnavailable = errors.New("service SID is unavailable")
)

func SecureServiceDataDirectory(path, serviceName string) error {
	if err := validateServiceDataOwners(path); err != nil {
		return err
	}
	serviceName = strings.TrimSpace(serviceName)
	if serviceName == "" {
		return ErrServiceSIDUnavailable
	}
	serviceSID, _, _, err := winapi.LookupSID("", `NT SERVICE\`+serviceName)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrServiceSIDUnavailable, err)
	}
	if err := secureDataDirectory(path, serviceSID); err != nil {
		return err
	}
	trustedOwner, err := winapi.CreateWellKnownSid(winapi.WinBuiltinAdministratorsSid)
	if err != nil {
		return err
	}
	if err := winapi.SetNamedSecurityInfo(path, winapi.SE_FILE_OBJECT, winapi.OWNER_SECURITY_INFORMATION, trustedOwner, nil, nil, nil); err != nil {
		return fmt.Errorf("set trusted service data owner: %w", err)
	}
	return validateServiceDataOwners(path)
}

// Do not take over pre-positioned data. An administrator must inspect an
// untrusted tree before explicitly relocating it or changing its ownership.
func validateServiceDataOwners(path string) error {
	if strings.TrimSpace(path) == "" {
		return ErrDataDirectoryInvalid
	}
	return filepath.WalkDir(path, func(entryPath string, entry fs.DirEntry, walkErr error) error {
		if os.IsNotExist(walkErr) && entryPath == path {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return ErrDataDirectoryInvalid
		}
		descriptor, err := winapi.GetNamedSecurityInfo(entryPath, winapi.SE_FILE_OBJECT, winapi.OWNER_SECURITY_INFORMATION)
		if err != nil {
			return err
		}
		owner, _, err := descriptor.Owner()
		if err != nil {
			return err
		}
		if owner == nil || (!owner.IsWellKnown(winapi.WinLocalSystemSid) && !owner.IsWellKnown(winapi.WinBuiltinAdministratorsSid)) {
			return fmt.Errorf("%w: untrusted filesystem owner of %q", ErrDataDirectoryInvalid, entryPath)
		}
		return nil
	})
}

// Offline maintenance may trust persisted session state only when ordinary
// accounts cannot replace its database, WAL, policy or directory permissions.
func ValidateMaintenanceDataDirectory(path string) error {
	if err := validateServiceDataOwners(path); err != nil {
		return err
	}
	return filepath.WalkDir(path, func(entryPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		descriptor, err := winapi.GetNamedSecurityInfo(entryPath, winapi.SE_FILE_OBJECT, winapi.DACL_SECURITY_INFORMATION)
		if err != nil {
			return err
		}
		control, _, err := descriptor.Control()
		if err != nil {
			return err
		}
		if entryPath == path && control&winapi.SE_DACL_PROTECTED == 0 {
			return ErrDataDirectoryInvalid
		}
		acl, _, err := descriptor.DACL()
		if err != nil || acl == nil {
			return ErrDataDirectoryInvalid
		}
		const fileDeleteChild = 0x0040
		const writeRights = winapi.GENERIC_ALL | winapi.GENERIC_WRITE | winapi.WRITE_DAC | winapi.WRITE_OWNER | winapi.DELETE | winapi.FILE_WRITE_DATA | winapi.FILE_APPEND_DATA | winapi.FILE_WRITE_EA | winapi.FILE_WRITE_ATTRIBUTES | fileDeleteChild
		for index := uint32(0); index < uint32(acl.AceCount); index++ {
			var ace *winapi.ACCESS_ALLOWED_ACE
			if err := winapi.GetAce(acl, index, &ace); err != nil {
				return err
			}
			if ace.Header.AceType == winapi.ACCESS_DENIED_ACE_TYPE {
				continue
			}
			if ace.Header.AceType != winapi.ACCESS_ALLOWED_ACE_TYPE {
				return ErrDataDirectoryInvalid
			}
			if ace.Mask&writeRights == 0 {
				continue
			}
			sid := (*winapi.SID)(unsafe.Pointer(&ace.SidStart))
			if !sid.IsWellKnown(winapi.WinLocalSystemSid) && !sid.IsWellKnown(winapi.WinBuiltinAdministratorsSid) && !strings.HasPrefix(sid.String(), "S-1-5-80-") {
				return fmt.Errorf("%w: ordinary-account write access to %q", ErrDataDirectoryInvalid, entryPath)
			}
		}
		return nil
	})
}

func SecureOwnerDirectory(path, ownerUserSID string) error {
	ownerUserSID = strings.TrimSpace(ownerUserSID)
	ownerSID, err := winapi.StringToSid(ownerUserSID)
	if err != nil || ownerSID == nil || !ownerSID.IsValid() || !strings.EqualFold(ownerSID.String(), ownerUserSID) {
		return ErrDataDirectoryInvalid
	}
	return secureDataDirectory(path, ownerSID)
}

func secureDataDirectory(path string, serviceSID *winapi.SID) error {
	path = strings.TrimSpace(path)
	if path == "" || serviceSID == nil || !serviceSID.IsValid() {
		return ErrDataDirectoryInvalid
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve service data directory: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return fmt.Errorf("create service data directory: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return fmt.Errorf("resolve service data directory links: %w", err)
	}
	if !strings.EqualFold(filepath.Clean(resolved), filepath.Clean(absolute)) {
		return ErrDataDirectoryInvalid
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrDataDirectoryInvalid
	}

	systemSID, err := winapi.CreateWellKnownSid(winapi.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("create LocalSystem SID: %w", err)
	}
	administratorsSID, err := winapi.CreateWellKnownSid(winapi.WinBuiltinAdministratorsSid)
	if err != nil {
		return fmt.Errorf("create Administrators SID: %w", err)
	}
	entries := []winapi.EXPLICIT_ACCESS{
		fullDirectoryAccess(systemSID),
		fullDirectoryAccess(administratorsSID),
		fullDirectoryAccess(serviceSID),
	}
	ownerRightsSID, err := winapi.StringToSid("S-1-3-4")
	if err != nil {
		return err
	}
	ownerRights := fullDirectoryAccess(ownerRightsSID)
	ownerRights.AccessPermissions = winapi.READ_CONTROL
	entries = append(entries, ownerRights)
	acl, err := winapi.ACLFromEntries(entries, nil)
	if err != nil {
		return fmt.Errorf("build service data directory ACL: %w", err)
	}
	if err := winapi.SetNamedSecurityInfo(
		absolute,
		winapi.SE_FILE_OBJECT,
		winapi.DACL_SECURITY_INFORMATION|winapi.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	); err != nil {
		return fmt.Errorf("set service data directory ACL: %w", err)
	}
	return nil
}

func fullDirectoryAccess(sid *winapi.SID) winapi.EXPLICIT_ACCESS {
	return winapi.EXPLICIT_ACCESS{
		AccessPermissions: winapi.GENERIC_ALL,
		AccessMode:        winapi.SET_ACCESS,
		Inheritance:       winapi.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: winapi.TRUSTEE{
			TrusteeForm:  winapi.TRUSTEE_IS_SID,
			TrusteeType:  winapi.TRUSTEE_IS_WELL_KNOWN_GROUP,
			TrusteeValue: winapi.TrusteeValueFromSID(sid),
		},
	}
}
