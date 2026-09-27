package maintenance

import (
	"errors"
	"fmt"
	"unsafe"

	winapi "golang.org/x/sys/windows"
)

func RestoreRecordedSystemChanges(dataDirectory string) error {
	journal, err := LoadRestorationJournal(dataDirectory)
	if err != nil {
		return err
	}
	if len(journal.Entries) == 0 {
		return nil
	}
	if err := enableSecurityPrivilege(); err != nil {
		return fmt.Errorf("enable security privilege: %w", err)
	}
	return restoreJournal(journal, restoreSystemEntry)
}

func restoreSystemEntry(entry RestorationEntry) error {
	switch entry.Kind {
	case RestorationKindFileSACL:
		return restoreFileSACL(entry.Target, entry.OriginalSDDL)
	default:
		return ErrRestorationJournalInvalid
	}
}

func restoreFileSACL(path, originalSDDL string) error {
	descriptor, err := winapi.SecurityDescriptorFromString(originalSDDL)
	if err != nil {
		return fmt.Errorf("decode original security descriptor: %w", err)
	}
	sacl, _, err := descriptor.SACL()
	if err != nil && !errors.Is(err, winapi.ERROR_OBJECT_NOT_FOUND) {
		return fmt.Errorf("read original SACL: %w", err)
	}
	information := winapi.SECURITY_INFORMATION(winapi.SACL_SECURITY_INFORMATION)
	control, _, err := descriptor.Control()
	if err != nil {
		return fmt.Errorf("read original SACL control: %w", err)
	}
	if control&winapi.SE_SACL_PROTECTED != 0 {
		information |= winapi.PROTECTED_SACL_SECURITY_INFORMATION
	} else {
		information |= winapi.UNPROTECTED_SACL_SECURITY_INFORMATION
	}
	if err := winapi.SetNamedSecurityInfo(path, winapi.SE_FILE_OBJECT, information, nil, nil, nil, sacl); err != nil {
		return fmt.Errorf("apply original SACL: %w", err)
	}
	return nil
}

func enableSecurityPrivilege() error {
	var luid winapi.LUID
	if err := winapi.LookupPrivilegeValue(nil, winapi.StringToUTF16Ptr("SeSecurityPrivilege"), &luid); err != nil {
		return err
	}
	privileges := winapi.Tokenprivileges{PrivilegeCount: 1}
	privileges.Privileges[0].Luid = luid
	privileges.Privileges[0].Attributes = winapi.SE_PRIVILEGE_ENABLED
	return winapi.AdjustTokenPrivileges(
		winapi.GetCurrentProcessToken(), false, &privileges,
		uint32(unsafe.Sizeof(privileges)), nil, nil,
	)
}
