package agent

import (
	"strings"
	"testing"
)

func TestInputShieldWindowsCredentialPromptUsesDedicatedMessage(t *testing.T) {
	if !strings.Contains(inputShieldSystemCredentialPromptMessage, "本地输入防护") ||
		strings.Contains(inputShieldSystemCredentialPromptMessage, "结束保护") {
		t.Fatalf("input shield system credential message = %q", inputShieldSystemCredentialPromptMessage)
	}
}

func TestInputShieldLocalPromptDoesNotPersistCredentials(t *testing.T) {
	if inputShieldPromptFlags&inputShieldPromptDoNotPersist == 0 {
		t.Fatal("local input shield prompt permits credential persistence")
	}
	if inputShieldPromptFlags&inputShieldPromptKeepUserName == 0 || inputShieldPromptFlags&inputShieldPromptGeneric == 0 {
		t.Fatal("local input shield prompt does not constrain the user name field")
	}
}

func TestInputShieldVerificationWindowRequiresCurrentProcess(t *testing.T) {
	if inputShieldVerificationWindowCandidate(0, 100) || inputShieldVerificationWindowCandidate(100, 0) {
		t.Fatal("zero verification window or process was accepted")
	}
}
