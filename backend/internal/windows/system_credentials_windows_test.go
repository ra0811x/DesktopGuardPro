package windows

import (
	"context"
	"errors"
	"testing"

	coreservice "desktopguardpro/internal/service"
)

func TestSystemCredentialVerifierAcceptsAuthenticatedOwner(t *testing.T) {
	password := []byte("secret")
	verifier := &SystemCredentialVerifier{dependencies: systemCredentialDependencies{
		logon: func(string, string, []byte) (authenticatedSystemToken, error) {
			return fakeAuthenticatedSystemToken{sid: "S-1-5-21-1000"}, nil
		},
	}}
	if err := verifier.VerifySystemCredentials(context.Background(), coreservice.SystemCredentials{
		UserName: "Raymond", Password: password,
	}, "S-1-5-21-1000"); err != nil {
		t.Fatalf("VerifySystemCredentials() error = %v", err)
	}
	if password[0] != 0 {
		t.Fatalf("password buffer was not cleared: %q", password)
	}
}

func TestSystemCredentialVerifierRejectsAuthenticatedDifferentUser(t *testing.T) {
	verifier := &SystemCredentialVerifier{dependencies: systemCredentialDependencies{
		logon: func(string, string, []byte) (authenticatedSystemToken, error) {
			return fakeAuthenticatedSystemToken{sid: "S-1-5-21-2000"}, nil
		},
	}}
	err := verifier.VerifySystemCredentials(context.Background(), coreservice.SystemCredentials{
		UserName: "Raymond", Password: []byte("secret"),
	}, "S-1-5-21-1000")
	if !errors.Is(err, ErrSystemCredentialIdentityMismatch) {
		t.Fatalf("VerifySystemCredentials() error = %v, want %v", err, ErrSystemCredentialIdentityMismatch)
	}
}

func TestEndSessionCredentialPromptDoesNotForceSecureDesktop(t *testing.T) {
	if endSessionCredentialPromptFlags&credUIWinSecurePrompt != 0 {
		t.Fatal("end-session credential prompt forces the secure desktop")
	}
	if endSessionCredentialPromptFlags&credUIWinGeneric != 0 {
		t.Fatal("end-session credential prompt requests plain-text generic credentials")
	}
}

type fakeAuthenticatedSystemToken struct{ sid string }

func (token fakeAuthenticatedSystemToken) UserSID() (string, error) { return token.sid, nil }
func (fakeAuthenticatedSystemToken) Close() error                   { return nil }
