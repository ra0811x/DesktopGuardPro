package agent

import (
	"context"
	"desktopguardpro/internal/domain"
	coreservice "desktopguardpro/internal/service"
	"errors"
	"testing"
)

func TestDeskGuardPasswordDialogUsesSavedCredentialVerifier(t *testing.T) {
	original := runDeskGuardPasswordDialog
	defer func() { runDeskGuardPasswordDialog = original }()
	runDeskGuardPasswordDialog = func(ctx context.Context, check func(string) bool) bool {
		if check("wrong") {
			t.Fatal("wrong password accepted")
		}
		return check("saved-password")
	}
	policy := domain.DefaultMonitoringPolicy().InputShield
	policy.CredentialMode = domain.InputShieldCredentialLocal
	policy.AllowRecoveryCode = true
	var secretBuffers [][]byte
	err := promptInputShieldCredentials(context.Background(), policy, func(request coreservice.InputShieldCredentialVerifyRequest) error {
		secretBuffers = append(secretBuffers, request.Password)
		if !request.AcceptRecovery {
			t.Fatal("single field must accept password or recovery")
		}
		if string(request.Password) == "saved-password" {
			return nil
		}
		return errors.New("verification_failed")
	})
	if err != nil || len(secretBuffers) != 2 {
		t.Fatalf("retry flow failed: %v", err)
	}
	for _, buffer := range secretBuffers {
		for _, b := range buffer {
			if b != 0 {
				t.Fatal("password buffer retained")
			}
		}
	}
}

func TestDeskGuardCancelledPromptNeverVerifies(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := promptInputShieldCredentials(ctx, domain.DefaultMonitoringPolicy().InputShield,
		func(coreservice.InputShieldCredentialVerifyRequest) error {
			t.Fatal("cancelled task attempted verification")
			return nil
		})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error: %v", err)
	}
}
