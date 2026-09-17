package maintenance

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unsafe"

	"desktopguardpro/internal/desktop"

	winapi "golang.org/x/sys/windows"
)

const (
	nativeMachineAMD64   = uint16(0x8664)
	windows10Build22H2   = uint32(19045)
	windows11Build22H2   = uint32(22621)
	windows11Build23H2   = uint32(22631)
	minimumWindowsMajor  = uint32(10)
	minimumWebView2Major = uint64(109)
	installComponentKind = "Authenticode"
)

var (
	ErrAdministratorRequired     = errors.New("administrator elevation is required")
	ErrArchitectureUnsupported   = errors.New("native Windows architecture is unsupported")
	ErrWindowsVersionUnsupported = errors.New("Windows version is unsupported")
	ErrComponentUntrusted        = errors.New("install component signature is untrusted")
	ErrInstallRootUntrusted      = errors.New("install directory is outside Program Files")
)

type PreflightReport struct {
	WindowsMajor    uint32
	WindowsMinor    uint32
	WindowsBuild    uint32
	NativeMachine   uint16
	WebView2Version string
	VerifiedFiles   []string
	SignerSHA256    string
	SignerSubject   string
}

type SignatureIdentity struct {
	SHA256  string
	Subject string
}

func VerifyAuthenticodeComponent(path string) (SignatureIdentity, error) {
	return (windowsPreflightProbe{}).VerifyComponent(path)
}

type preflightProbe interface {
	IsElevated() bool
	WindowsVersion() (major, minor, build uint32)
	NativeMachine() (uint16, error)
	WebView2Version() (string, error)
	TrustInstallDirectory(path string) error
	VerifyComponent(path string) (SignatureIdentity, error)
}

func RunWindowsInstallPreflight(options ValidatedInstallOptions) (PreflightReport, error) {
	return runInstallPreflight(windowsPreflightProbe{}, options)
}

func runInstallPreflight(probe preflightProbe, options ValidatedInstallOptions) (PreflightReport, error) {
	if !probe.IsElevated() {
		return PreflightReport{}, ErrAdministratorRequired
	}
	major, minor, build := probe.WindowsVersion()
	if major != minimumWindowsMajor || (build != windows10Build22H2 && build < windows11Build22H2) {
		return PreflightReport{}, fmt.Errorf("%w: %d.%d.%d", ErrWindowsVersionUnsupported, major, minor, build)
	}
	nativeMachine, err := probe.NativeMachine()
	if err != nil {
		return PreflightReport{}, fmt.Errorf("detect native architecture: %w", err)
	}
	if nativeMachine != nativeMachineAMD64 {
		return PreflightReport{}, fmt.Errorf("%w: machine=0x%04x", ErrArchitectureUnsupported, nativeMachine)
	}
	if err := probe.TrustInstallDirectory(options.InstallDirectory); err != nil {
		return PreflightReport{}, err
	}
	webView2Version, err := probe.WebView2Version()
	if err != nil {
		return PreflightReport{}, fmt.Errorf("detect WebView2 Runtime: %w", err)
	}
	if !supportedWebView2Version(webView2Version) {
		return PreflightReport{}, fmt.Errorf("%w: WebView2 %q", ErrWindowsVersionUnsupported, webView2Version)
	}

	components := []string{options.ServiceExecutable, options.UIExecutable, options.AgentExecutable, options.maintenanceExecutable()}
	verifiedFiles := make([]string, 0, len(components))
	var signer SignatureIdentity
	for _, component := range components {
		identity, err := probe.VerifyComponent(component)
		if err != nil {
			return PreflightReport{}, fmt.Errorf("%w: %s file %q: %v", ErrComponentUntrusted, installComponentKind, component, err)
		}
		if identity.SHA256 == "" || identity.Subject == "" {
			return PreflightReport{}, fmt.Errorf("%w: signer identity for %q is empty", ErrComponentUntrusted, component)
		}
		if signer.SHA256 == "" {
			signer = identity
		} else if !strings.EqualFold(signer.SHA256, identity.SHA256) {
			return PreflightReport{}, fmt.Errorf("%w: component signer mismatch for %q", ErrComponentUntrusted, component)
		}
		verifiedFiles = append(verifiedFiles, component)
	}
	return PreflightReport{
		WindowsMajor:    major,
		WindowsMinor:    minor,
		WindowsBuild:    build,
		NativeMachine:   nativeMachine,
		WebView2Version: webView2Version,
		VerifiedFiles:   verifiedFiles,
		SignerSHA256:    signer.SHA256,
		SignerSubject:   signer.Subject,
	}, nil
}

func supportedWebView2Version(version string) bool {
	parts := strings.Split(strings.TrimSpace(version), ".")
	if len(parts) < 4 {
		return false
	}
	major, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil || major < minimumWebView2Major {
		return false
	}
	for _, part := range parts[1:] {
		if _, err := strconv.ParseUint(part, 10, 32); err != nil {
			return false
		}
	}
	return true
}

type windowsPreflightProbe struct{}

func (windowsPreflightProbe) IsElevated() bool {
	return winapi.GetCurrentProcessToken().IsElevated()
}

func (windowsPreflightProbe) WindowsVersion() (uint32, uint32, uint32) {
	version := winapi.RtlGetVersion()
	return version.MajorVersion, version.MinorVersion, version.BuildNumber
}

func (windowsPreflightProbe) NativeMachine() (uint16, error) {
	var processMachine uint16
	var nativeMachine uint16
	if err := winapi.IsWow64Process2(winapi.CurrentProcess(), &processMachine, &nativeMachine); err != nil {
		return 0, err
	}
	return nativeMachine, nil
}

func (windowsPreflightProbe) WebView2Version() (string, error) {
	return desktop.DetectWebView2Runtime()
}

func (windowsPreflightProbe) TrustInstallDirectory(path string) error {
	programFiles, err := winapi.KnownFolderPath(winapi.FOLDERID_ProgramFiles, winapi.KF_FLAG_DEFAULT)
	if err != nil {
		return fmt.Errorf("resolve Program Files directory: %w", err)
	}
	if !pathWithin(programFiles, path) {
		return fmt.Errorf("%w: %q", ErrInstallRootUntrusted, path)
	}
	return nil
}

func (windowsPreflightProbe) VerifyComponent(path string) (SignatureIdentity, error) {
	pathUTF16, err := winapi.UTF16PtrFromString(path)
	if err != nil {
		return SignatureIdentity{}, err
	}
	fileInfo := &winapi.WinTrustFileInfo{
		Size:     uint32(unsafe.Sizeof(winapi.WinTrustFileInfo{})),
		FilePath: pathUTF16,
	}
	trustData := &winapi.WinTrustData{
		Size:                            uint32(unsafe.Sizeof(winapi.WinTrustData{})),
		UIChoice:                        winapi.WTD_UI_NONE,
		RevocationChecks:                winapi.WTD_REVOKE_WHOLECHAIN,
		UnionChoice:                     winapi.WTD_CHOICE_FILE,
		StateAction:                     winapi.WTD_STATEACTION_VERIFY,
		ProvFlags:                       winapi.WTD_REVOCATION_CHECK_CHAIN_EXCLUDE_ROOT,
		UIContext:                       winapi.WTD_UICONTEXT_INSTALL,
		FileOrCatalogOrBlobOrSgnrOrCert: unsafe.Pointer(fileInfo),
	}
	verifyErr := winapi.WinVerifyTrustEx(winapi.InvalidHWND, &winapi.WINTRUST_ACTION_GENERIC_VERIFY_V2, trustData)
	var identity SignatureIdentity
	if verifyErr == nil {
		identity, verifyErr = signatureIdentityFromTrustState(trustData.StateData)
	}
	trustData.StateAction = winapi.WTD_STATEACTION_CLOSE
	closeErr := winapi.WinVerifyTrustEx(winapi.InvalidHWND, &winapi.WINTRUST_ACTION_GENERIC_VERIFY_V2, trustData)
	return identity, errors.Join(verifyErr, closeErr)
}

var (
	wintrustDLL                     = winapi.NewLazySystemDLL("wintrust.dll")
	procProvDataFromStateData       = wintrustDLL.NewProc("WTHelperProvDataFromStateData")
	procGetProvSignerFromChain      = wintrustDLL.NewProc("WTHelperGetProvSignerFromChain")
	procGetProvCertificateFromChain = wintrustDLL.NewProc("WTHelperGetProvCertFromChain")
)

type cryptProviderCertificateHeader struct {
	Size        uint32
	Certificate *winapi.CertContext
}

func signatureIdentityFromTrustState(stateData winapi.Handle) (SignatureIdentity, error) {
	if stateData == 0 {
		return SignatureIdentity{}, errors.New("WinTrust state data is unavailable")
	}
	providerData, _, _ := procProvDataFromStateData.Call(uintptr(stateData))
	if providerData == 0 {
		return SignatureIdentity{}, errors.New("WinTrust provider data is unavailable")
	}
	signer, _, _ := procGetProvSignerFromChain.Call(providerData, 0, 0, 0)
	if signer == 0 {
		return SignatureIdentity{}, errors.New("WinTrust signer is unavailable")
	}
	providerCertificate, _, _ := procGetProvCertificateFromChain.Call(signer, 0)
	if providerCertificate == 0 {
		return SignatureIdentity{}, errors.New("WinTrust signer certificate is unavailable")
	}
	certificate := certificateContextFromProvider(providerCertificate)
	if certificate == nil || certificate.EncodedCert == nil || certificate.Length == 0 {
		return SignatureIdentity{}, errors.New("WinTrust signer certificate is invalid")
	}
	encoded := unsafe.Slice(certificate.EncodedCert, certificate.Length)
	digest := sha256.Sum256(encoded)
	subject, err := certificateDisplayName(certificate)
	if err != nil {
		return SignatureIdentity{}, err
	}
	return SignatureIdentity{SHA256: hex.EncodeToString(digest[:]), Subject: subject}, nil
}

func certificateContextFromProvider(address uintptr) *winapi.CertContext {
	pointer := *(*unsafe.Pointer)(unsafe.Pointer(&address))
	return (*cryptProviderCertificateHeader)(pointer).Certificate
}

func certificateDisplayName(certificate *winapi.CertContext) (string, error) {
	characters := winapi.CertGetNameString(certificate, winapi.CERT_NAME_SIMPLE_DISPLAY_TYPE, 0, nil, nil, 0)
	if characters < 2 {
		return "", errors.New("signer certificate subject is unavailable")
	}
	buffer := make([]uint16, characters)
	if winapi.CertGetNameString(certificate, winapi.CERT_NAME_SIMPLE_DISPLAY_TYPE, 0, nil, &buffer[0], characters) != characters {
		return "", errors.New("read signer certificate subject")
	}
	return strings.TrimSpace(winapi.UTF16ToString(buffer)), nil
}
