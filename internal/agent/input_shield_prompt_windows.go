package agent

import (
	"context"
	"errors"

	"desktopguardpro/internal/deskguard/unlock"
	"desktopguardpro/internal/domain"
	coreservice "desktopguardpro/internal/service"
	platformwindows "desktopguardpro/internal/windows"
)

var ErrInputShieldPromptCancelled = errors.New("input shield credential prompt was cancelled")
var runDeskGuardPasswordDialog = unlock.Run

type inputShieldVerify func(coreservice.InputShieldCredentialVerifyRequest) error

const inputShieldSystemCredentialPromptMessage = "解除本地输入防护需要确认 Windows 所有者凭据。验证期间键鼠保持可用，取消后恢复输入控制。"

func promptInputShieldCredentials(ctx context.Context, policy domain.InputShieldPolicy, verify inputShieldVerify) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if policy.CredentialMode == domain.InputShieldCredentialWindows {
		credentials, err := platformwindows.PromptForSystemCredentialsWithMessage(inputShieldSystemCredentialPromptMessage)
		if err != nil {
			return err
		}
		defer clear(credentials.Password)
		return verify(coreservice.InputShieldCredentialVerifyRequest{UserName: credentials.UserName,
			Domain: credentials.Domain, Password: credentials.Password})
	}
	verified := runDeskGuardPasswordDialog(ctx, func(secret string) bool {
		password := []byte(secret)
		defer clear(password)
		if ctx.Err() != nil {
			return false
		}
		return verify(coreservice.InputShieldCredentialVerifyRequest{
			Password: password, AcceptRecovery: policy.AllowRecoveryCode,
		}) == nil
	})
	if err := ctx.Err(); err != nil {
		return err
	}
	if !verified {
		return ErrInputShieldPromptCancelled
	}
	return nil
}
