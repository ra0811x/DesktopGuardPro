package maintenance

import (
	"errors"
	"strings"
	"testing"
)

func TestRunInstallPreflightAcceptsSupportedSignedComponents(t *testing.T) {
	options := preflightOptions()
	probe := supportedPreflightProbe()
	probe.build = windows11Build23H2

	report, err := runInstallPreflight(probe, options)
	if err != nil {
		t.Fatalf("runInstallPreflight() error = %v", err)
	}
	if report.WindowsBuild != windows11Build23H2 || report.NativeMachine != nativeMachineAMD64 || report.WebView2Version != probe.webView2 {
		t.Fatalf("preflight report = %#v", report)
	}
	if len(report.VerifiedFiles) != 4 || len(probe.verified) != 4 {
		t.Fatalf("verified files = %#v probe=%#v", report.VerifiedFiles, probe.verified)
	}
	if report.SignerSHA256 != probe.signer.SHA256 || report.SignerSubject != probe.signer.Subject {
		t.Fatalf("signer identity = %q %q", report.SignerSHA256, report.SignerSubject)
	}
}

func TestRunInstallPreflightAcceptsWindows11Build22H2(t *testing.T) {
	probe := supportedPreflightProbe()
	const windows11Build22H2 = uint32(22621)
	probe.build = windows11Build22H2

	report, err := runInstallPreflight(probe, preflightOptions())
	if err != nil {
		t.Fatalf("runInstallPreflight() error = %v", err)
	}
	if report.WindowsBuild != windows11Build22H2 {
		t.Fatalf("Windows build = %d, want %d", report.WindowsBuild, windows11Build22H2)
	}
}

func TestRunInstallPreflightRejectsUnsupportedEnvironment(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*fakePreflightProbe)
		wantErr error
	}{
		{name: "not elevated", mutate: func(probe *fakePreflightProbe) { probe.elevated = false }, wantErr: ErrAdministratorRequired},
		{name: "old Windows 10", mutate: func(probe *fakePreflightProbe) { probe.build = windows10Build22H2 - 1 }, wantErr: ErrWindowsVersionUnsupported},
		{name: "old Windows 11", mutate: func(probe *fakePreflightProbe) { probe.build = 22000 }, wantErr: ErrWindowsVersionUnsupported},
		{name: "arm64", mutate: func(probe *fakePreflightProbe) { probe.nativeMachine = 0xaa64 }, wantErr: ErrArchitectureUnsupported},
		{name: "untrusted install root", mutate: func(probe *fakePreflightProbe) { probe.installRootErr = ErrInstallRootUntrusted }, wantErr: ErrInstallRootUntrusted},
		{name: "missing WebView2", mutate: func(probe *fakePreflightProbe) { probe.webViewErr = errors.New("missing") }, wantErr: probeWebViewError{}},
		{name: "old WebView2", mutate: func(probe *fakePreflightProbe) { probe.webView2 = "108.0.1462.76" }, wantErr: ErrWindowsVersionUnsupported},
		{name: "invalid WebView2", mutate: func(probe *fakePreflightProbe) { probe.webView2 = "current" }, wantErr: ErrWindowsVersionUnsupported},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			probe := supportedPreflightProbe()
			test.mutate(probe)
			_, err := runInstallPreflight(probe, preflightOptions())
			if test.name == "missing WebView2" {
				if err == nil || !errors.Is(err, probe.webViewErr) {
					t.Fatalf("runInstallPreflight() error = %v, want wrapped WebView2 error", err)
				}
				return
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("runInstallPreflight() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestRunInstallPreflightRejectsFirstUntrustedComponent(t *testing.T) {
	probe := supportedPreflightProbe()
	probe.untrustedPath = preflightOptions().UIExecutable

	_, err := runInstallPreflight(probe, preflightOptions())
	if !errors.Is(err, ErrComponentUntrusted) {
		t.Fatalf("runInstallPreflight() error = %v, want %v", err, ErrComponentUntrusted)
	}
	if len(probe.verified) != 2 {
		t.Fatalf("signature verification calls = %#v", probe.verified)
	}
}

func TestRunInstallPreflightRejectsMixedPublishers(t *testing.T) {
	probe := supportedPreflightProbe()
	probe.mismatchPath = preflightOptions().AgentExecutable
	_, err := runInstallPreflight(probe, preflightOptions())
	if !errors.Is(err, ErrComponentUntrusted) {
		t.Fatalf("runInstallPreflight() error = %v, want %v", err, ErrComponentUntrusted)
	}
}

func supportedPreflightProbe() *fakePreflightProbe {
	return &fakePreflightProbe{
		elevated:      true,
		major:         10,
		build:         windows10Build22H2,
		nativeMachine: nativeMachineAMD64,
		webView2:      "151.0.4129.101",
		signer:        SignatureIdentity{SHA256: strings.Repeat("a", 64), Subject: "Desktop Guard Pro Test"},
	}
}

func preflightOptions() ValidatedInstallOptions {
	return ValidatedInstallOptions{
		ServiceExecutable: `C:\Program Files\Desktop Guard Pro\desktop-guard-service.exe`,
		UIExecutable:      `C:\Program Files\Desktop Guard Pro\desktop-guard-ui.exe`,
		AgentExecutable:   `C:\Program Files\Desktop Guard Pro\desktop-guard-agent.exe`,
	}
}

type probeWebViewError struct{}

func (probeWebViewError) Error() string { return "WebView2 error" }

type fakePreflightProbe struct {
	elevated        bool
	major           uint32
	minor           uint32
	build           uint32
	nativeMachine   uint16
	architectureErr error
	installRootErr  error
	webView2        string
	webViewErr      error
	untrustedPath   string
	mismatchPath    string
	signer          SignatureIdentity
	verified        []string
}

func (probe *fakePreflightProbe) IsElevated() bool { return probe.elevated }

func (probe *fakePreflightProbe) WindowsVersion() (uint32, uint32, uint32) {
	return probe.major, probe.minor, probe.build
}

func (probe *fakePreflightProbe) NativeMachine() (uint16, error) {
	return probe.nativeMachine, probe.architectureErr
}

func (probe *fakePreflightProbe) WebView2Version() (string, error) {
	return probe.webView2, probe.webViewErr
}

func (probe *fakePreflightProbe) TrustInstallDirectory(string) error {
	return probe.installRootErr
}

func (probe *fakePreflightProbe) VerifyComponent(path string) (SignatureIdentity, error) {
	probe.verified = append(probe.verified, path)
	if path == probe.untrustedPath {
		return SignatureIdentity{}, errors.New("untrusted")
	}
	if path == probe.mismatchPath {
		return SignatureIdentity{SHA256: strings.Repeat("b", 64), Subject: "Other Publisher"}, nil
	}
	return probe.signer, nil
}
