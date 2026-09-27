package installpolicy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	winapi "golang.org/x/sys/windows"
)

const (
	FileName          = "install-policy.json"
	CurrentVersion    = 1
	maximumPolicySize = 4096
)

var (
	ErrPolicyInvalid       = errors.New("install policy is invalid")
	ErrPolicyOwnerMismatch = errors.New("install policy owner does not match")
)

type Policy struct {
	Version      int    `json:"version"`
	OwnerUserSID string `json:"ownerUserSid"`
}

func NewForCurrentUser() (Policy, error) {
	user, err := winapi.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user.User.Sid == nil || !user.User.Sid.IsValid() {
		return Policy{}, fmt.Errorf("read current user SID: %w", err)
	}
	return New(user.User.Sid.String())
}

func New(ownerUserSID string) (Policy, error) {
	policy := Policy{Version: CurrentVersion, OwnerUserSID: strings.TrimSpace(ownerUserSID)}
	if err := policy.Validate(); err != nil {
		return Policy{}, err
	}
	return policy, nil
}

func (policy Policy) Validate() error {
	if policy.Version != CurrentVersion || policy.OwnerUserSID == "" {
		return ErrPolicyInvalid
	}
	sid, err := winapi.StringToSid(policy.OwnerUserSID)
	if err != nil || sid == nil || !sid.IsValid() || !strings.EqualFold(sid.String(), policy.OwnerUserSID) {
		return ErrPolicyInvalid
	}
	return nil
}

func Save(dataDirectory string, policy Policy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	encoded, err := json.Marshal(policy)
	if err != nil {
		return fmt.Errorf("encode install policy: %w", err)
	}
	encoded = append(encoded, '\n')
	if len(encoded) > maximumPolicySize {
		return ErrPolicyInvalid
	}

	temporary, err := os.CreateTemp(dataDirectory, ".install-policy-*")
	if err != nil {
		return fmt.Errorf("create temporary install policy: %w", err)
	}
	temporaryPath := temporary.Name()
	keepTemporary := true
	defer func() {
		_ = temporary.Close()
		if keepTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("restrict temporary install policy: %w", err)
	}
	if _, err := temporary.Write(encoded); err != nil {
		return fmt.Errorf("write temporary install policy: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("flush temporary install policy: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary install policy: %w", err)
	}
	target := filepath.Join(dataDirectory, FileName)
	if err := os.Rename(temporaryPath, target); err != nil {
		return fmt.Errorf("replace install policy: %w", err)
	}
	keepTemporary = false
	return nil
}

func EnsureOwner(dataDirectory string, policy Policy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	path := filepath.Join(dataDirectory, FileName)
	_, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return Save(dataDirectory, policy)
	}
	if err != nil {
		return fmt.Errorf("inspect install policy: %w", err)
	}
	existing, err := Load(dataDirectory)
	if err != nil {
		return err
	}
	if !strings.EqualFold(existing.OwnerUserSID, policy.OwnerUserSID) {
		return ErrPolicyOwnerMismatch
	}
	return nil
}

func Load(dataDirectory string) (Policy, error) {
	path := filepath.Join(dataDirectory, FileName)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maximumPolicySize {
		return Policy{}, fmt.Errorf("%w: policy file", ErrPolicyInvalid)
	}
	file, err := os.Open(path)
	if err != nil {
		return Policy{}, fmt.Errorf("open install policy: %w", err)
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maximumPolicySize+1))
	if err != nil || len(encoded) > maximumPolicySize {
		return Policy{}, fmt.Errorf("%w: policy content", ErrPolicyInvalid)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var policy Policy
	if err := decoder.Decode(&policy); err != nil {
		return Policy{}, fmt.Errorf("%w: %v", ErrPolicyInvalid, err)
	}
	if err := ensureEOF(decoder); err != nil {
		return Policy{}, err
	}
	if err := policy.Validate(); err != nil {
		return Policy{}, err
	}
	return policy, nil
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrPolicyInvalid
	}
	return nil
}
