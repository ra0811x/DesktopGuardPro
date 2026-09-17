package collector

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"

	"desktopguardpro/internal/domain"
)

const fileSystemAuditPolicySubcategory = "{0CCE921D-69AE-11D9-BED3-505054503030}"

type fileSystemAuditPolicyState struct {
	Success bool
	Failure bool
}

var setFileSystemAuditPolicy = func(ctx context.Context, state fileSystemAuditPolicyState) error {
	enabled := func(value bool) string {
		if value {
			return "enable"
		}
		return "disable"
	}
	output, err := exec.CommandContext(
		ctx, "auditpol.exe", "/set", "/subcategory:"+fileSystemAuditPolicySubcategory,
		"/success:"+enabled(state.Success), "/failure:"+enabled(state.Failure),
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("set File System audit policy: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func prepareFileSystemAuditPolicy(ctx context.Context) (func() error, error) {
	output, err := queryStrictAuditPolicy(ctx)
	if err != nil {
		return nil, fmt.Errorf("read File System audit policy: %w", err)
	}
	original, err := parseFileSystemAuditPolicyState(string(output))
	if err != nil {
		return nil, err
	}
	if original.Success {
		return func() error { return nil }, nil
	}
	enabled := original
	enabled.Success = true
	if err := setFileSystemAuditPolicy(ctx, enabled); err != nil {
		return nil, err
	}
	return func() error {
		restoreContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return setFileSystemAuditPolicy(restoreContext, original)
	}, nil
}

func parseFileSystemAuditPolicyState(output string) (fileSystemAuditPolicyState, error) {
	normalized := strings.ToLower(strings.TrimSpace(output))
	if normalized == "" {
		return fileSystemAuditPolicyState{}, errors.New("File System audit policy result is empty")
	}
	if strings.Contains(normalized, "no auditing") || strings.Contains(normalized, "无审核") {
		return fileSystemAuditPolicyState{}, nil
	}
	state := fileSystemAuditPolicyState{
		Success: strings.Contains(normalized, "success") || strings.Contains(normalized, "成功"),
		Failure: strings.Contains(normalized, "failure") || strings.Contains(normalized, "失败"),
	}
	if !state.Success && !state.Failure {
		return fileSystemAuditPolicyState{}, errors.New("File System audit policy result is unrecognized")
	}
	return state, nil
}

type strictAuditACLBackup struct {
	path string
	sddl string
}

func configureStrictReadAuditTargets(ctx context.Context, targets []domain.MonitoringTarget) (func() error, error) {
	restorePolicy, err := prepareFileSystemAuditPolicy(ctx)
	if err != nil {
		return nil, err
	}
	backups := make([]strictAuditACLBackup, 0, len(targets))
	for _, target := range targets {
		path := target.Path
		recursive := target.Recursive || target.Kind == domain.MonitoringTargetKindRemovableVolume
		if target.Kind == domain.MonitoringTargetKindFile {
			if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
				path = filepath.Dir(path)
				recursive = false
			}
		}
		sddl, err := addStrictReadAuditRule(ctx, path, recursive)
		if err != nil {
			_ = errors.Join(restoreStrictReadAuditACLs(backups), restorePolicy())
			return nil, fmt.Errorf("configure audit ACL for %s: %w", path, err)
		}
		backups = append(backups, strictAuditACLBackup{path: path, sddl: sddl})
	}
	return func() error { return errors.Join(restoreStrictReadAuditACLs(backups), restorePolicy()) }, nil
}

func addStrictReadAuditRule(ctx context.Context, path string, recursive bool) (string, error) {
	inheritance := "[Security.AccessControl.InheritanceFlags]::None"
	if recursive {
		inheritance = "[Security.AccessControl.InheritanceFlags]::ContainerInherit -bor [Security.AccessControl.InheritanceFlags]::ObjectInherit"
	}
	script := fmt.Sprintf(`$ErrorActionPreference='Stop'
$path=[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s'))
$acl=Get-Acl -LiteralPath $path -Audit
$sddl=$acl.GetSecurityDescriptorSddlForm([Security.AccessControl.AccessControlSections]::Audit)
$sid=New-Object Security.Principal.SecurityIdentifier('S-1-1-0')
$rights=[Security.AccessControl.FileSystemRights]::ReadData -bor [Security.AccessControl.FileSystemRights]::ReadAttributes -bor [Security.AccessControl.FileSystemRights]::ReadExtendedAttributes -bor [Security.AccessControl.FileSystemRights]::ReadPermissions
$rule=[Security.AccessControl.FileSystemAuditRule]::new($sid,$rights,(%s),[Security.AccessControl.PropagationFlags]::None,[Security.AccessControl.AuditFlags]::Success)
$acl.AddAuditRule($rule)
Set-Acl -LiteralPath $path -AclObject $acl
[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($sddl))`, encodeUTF8(path), inheritance)
	output, err := runEncodedPowerShell(ctx, script)
	if err != nil {
		return "", err
	}
	lines := strings.Fields(strings.TrimSpace(string(output)))
	if len(lines) == 0 {
		return "", errors.New("audit ACL backup was not returned")
	}
	decoded, err := base64.StdEncoding.DecodeString(lines[len(lines)-1])
	if err != nil || len(decoded) == 0 {
		return "", errors.New("audit ACL backup is invalid")
	}
	return string(decoded), nil
}

func restoreStrictReadAuditACLs(backups []strictAuditACLBackup) error {
	var restoreErr error
	for index := len(backups) - 1; index >= 0; index-- {
		backup := backups[index]
		script := fmt.Sprintf(`$ErrorActionPreference='Stop'
$path=[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s'))
$sddl=[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s'))
$acl=Get-Acl -LiteralPath $path -Audit
$acl.SetSecurityDescriptorSddlForm($sddl,[Security.AccessControl.AccessControlSections]::Audit)
Set-Acl -LiteralPath $path -AclObject $acl`, encodeUTF8(backup.path), encodeUTF8(backup.sddl))
		if _, err := runEncodedPowerShell(context.Background(), script); err != nil {
			restoreErr = errors.Join(restoreErr, fmt.Errorf("restore audit ACL for %s: %w", backup.path, err))
		}
	}
	return restoreErr
}

func runEncodedPowerShell(ctx context.Context, script string) ([]byte, error) {
	units := utf16.Encode([]rune(script))
	bytes := make([]byte, len(units)*2)
	for index, unit := range units {
		bytes[index*2] = byte(unit)
		bytes[index*2+1] = byte(unit >> 8)
	}
	output, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(bytes)).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("PowerShell audit ACL command failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func encodeUTF8(value string) string {
	return base64.StdEncoding.EncodeToString([]byte(value))
}
