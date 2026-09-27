package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"desktopguardpro/internal/maintenance"
)

type releaseComponent struct {
	Name      string `json:"name"`
	FileName  string `json:"fileName"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	Signature string `json:"signature"`
}

type releaseAsset struct {
	FileName string `json:"fileName"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
}

type releaseManifest struct {
	SchemaVersion   int                `json:"schemaVersion"`
	ProductVersion  string             `json:"productVersion"`
	Target          string             `json:"target"`
	CreatedUTC      time.Time          `json:"createdUtc"`
	SigningRequired bool               `json:"signingRequired"`
	SigningCommand  string             `json:"signingCommand"`
	SignerSHA256    string             `json:"signerSha256,omitempty"`
	SignerSubject   string             `json:"signerSubject,omitempty"`
	Components      []releaseComponent `json:"components"`
	Assets          []releaseAsset     `json:"assets"`
}

const (
	applicationIconFileName   = "desktop-guard-pro.ico"
	thirdPartyNoticesFileName = "THIRD-PARTY-NOTICES.txt"
)

var componentCommands = []struct {
	name, fileName, packagePath string
	windowGUI                   bool
	nativeProject               bool
}{
	{name: "service", fileName: "desktop-guard-service.exe", packagePath: "./cmd/desktop-guard-service"},
	{name: "ui", fileName: "desktop-guard-ui.exe", packagePath: "../frontend/DesktopGuardPro.Native", windowGUI: true, nativeProject: true},
	{name: "agent", fileName: "desktop-guard-agent.exe", packagePath: "./cmd/desktop-guard-agent"},
	{name: "maintenance", fileName: "desktop-guard-maintenance.exe", packagePath: "./cmd/desktop-guard-maintenance", windowGUI: true},
}

func main() {
	version := flag.String("version", "", "numeric product version")
	output := flag.String("output", "../dist", "release output directory")
	finalize := flag.String("finalize", "", "signed release directory to verify and finalize")
	flag.Parse()
	if !validReleaseVersion(*version) {
		fmt.Fprintln(os.Stderr, "a numeric --version is required")
		os.Exit(2)
	}
	if *finalize != "" {
		directory, err := filepath.Abs(*finalize)
		if err == nil {
			err = finalizeRelease(directory, *version, time.Now().UTC(), maintenance.VerifyAuthenticodeComponent)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(directory)
		return
	}
	root, err := filepath.Abs(*output)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	releaseDirectory := filepath.Join(root, "DesktopGuardPro-"+*version+"-windows-amd64")
	if err := buildRelease(releaseDirectory, *version, time.Now().UTC()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(releaseDirectory)
}

func buildRelease(releaseDirectory, version string, createdUTC time.Time) error {
	if err := os.MkdirAll(releaseDirectory, 0o700); err != nil {
		return fmt.Errorf("create release directory: %w", err)
	}
	for _, component := range componentCommands {
		if component.nativeProject {
			if err := runCommand(
				"dotnet", "publish", component.packagePath,
				"--configuration", "Release", "-p:Platform=x64", "-p:PublishTrimmed=false",
				"-p:DebugType=None", "-p:DebugSymbols=false", "-p:Version="+version,
				"--output", releaseDirectory); err != nil {
				return err
			}
			continue
		}
		arguments := []string{"build", "-trimpath", "-buildvcs=false"}
		linkerFlags := "-s -w -X desktopguardpro/internal/buildinfo.Version=" + version
		if component.windowGUI {
			linkerFlags += " -H=windowsgui"
		}
		arguments = append(arguments, "-ldflags", linkerFlags, "-o", filepath.Join(releaseDirectory, component.fileName), component.packagePath)
		if err := runCommand("go", arguments...); err != nil {
			return err
		}
	}
	for _, assetName := range releaseAssetFileNames() {
		assetData, err := os.ReadFile(releaseAssetSourcePath(assetName))
		if err != nil {
			return fmt.Errorf("read release asset %q: %w", assetName, err)
		}
		if err := os.WriteFile(filepath.Join(releaseDirectory, assetName), assetData, 0o600); err != nil {
			return fmt.Errorf("copy release asset %q: %w", assetName, err)
		}
	}
	manifest, err := createReleaseManifest(releaseDirectory, version, createdUTC)
	if err != nil {
		return err
	}
	manifestPath := filepath.Join(releaseDirectory, "release-manifest.json")
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(manifestPath, append(encoded, '\n'), 0o600); err != nil {
		return err
	}
	names, err := releaseDirectoryFileNames(releaseDirectory)
	if err != nil {
		return err
	}
	return createReleaseZip(releaseDirectory, names)
}

func createReleaseManifest(directory, version string, createdUTC time.Time) (releaseManifest, error) {
	if !validReleaseVersion(version) || createdUTC.IsZero() {
		return releaseManifest{}, errors.New("invalid release metadata")
	}
	manifest := releaseManifest{
		SchemaVersion: 2, ProductVersion: version, Target: "windows/amd64", CreatedUTC: createdUTC.UTC(),
		SigningRequired: true,
		SigningCommand:  "signtool sign /fd SHA256 /td SHA256 /tr <timestamp-url> /sha1 <certificate-thumbprint> <component.exe>; desktop-guard-release --version <version> --finalize <release-directory>",
	}
	for _, component := range componentCommands {
		path := filepath.Join(directory, component.fileName)
		file, err := os.Open(path)
		if err != nil {
			return releaseManifest{}, err
		}
		info, statErr := file.Stat()
		digest := sha256.New()
		_, hashErr := io.Copy(digest, file)
		closeErr := file.Close()
		if err := errors.Join(statErr, hashErr, closeErr); err != nil || !info.Mode().IsRegular() || info.Size() < 1 {
			return releaseManifest{}, errors.New("invalid release component")
		}
		manifest.Components = append(manifest.Components, releaseComponent{
			Name: component.name, FileName: component.fileName, Size: info.Size(),
			SHA256: hex.EncodeToString(digest.Sum(nil)), Signature: "pending",
		})
	}
	for _, assetName := range releaseAssetFileNames() {
		asset, err := os.Open(filepath.Join(directory, assetName))
		if err != nil {
			return releaseManifest{}, err
		}
		assetInfo, statErr := asset.Stat()
		assetDigest := sha256.New()
		_, hashErr := io.Copy(assetDigest, asset)
		closeErr := asset.Close()
		if err := errors.Join(statErr, hashErr, closeErr); err != nil || !assetInfo.Mode().IsRegular() || assetInfo.Size() < 1 {
			return releaseManifest{}, errors.New("invalid release asset")
		}
		manifest.Assets = append(manifest.Assets, releaseAsset{
			FileName: assetName,
			Size:     assetInfo.Size(),
			SHA256:   hex.EncodeToString(assetDigest.Sum(nil)),
		})
	}
	return manifest, nil
}

type signatureVerifier func(string) (maintenance.SignatureIdentity, error)

func finalizeRelease(directory, version string, createdUTC time.Time, verify signatureVerifier) error {
	manifest, err := createReleaseManifest(directory, version, createdUTC)
	if err != nil {
		return err
	}
	var signer maintenance.SignatureIdentity
	for index := range manifest.Components {
		component := &manifest.Components[index]
		identity, err := verify(filepath.Join(directory, component.FileName))
		if err != nil {
			return fmt.Errorf("verify signed component %q: %w", component.Name, err)
		}
		if signer.SHA256 == "" {
			signer = identity
		} else if !strings.EqualFold(signer.SHA256, identity.SHA256) {
			return errors.New("release components use different signing certificates")
		}
		component.Signature = "trusted"
	}
	if signer.SHA256 == "" || signer.Subject == "" {
		return errors.New("release signer identity is empty")
	}
	manifest.SigningRequired = false
	manifest.SignerSHA256 = signer.SHA256
	manifest.SignerSubject = signer.Subject
	manifest.CreatedUTC = createdUTC.UTC()
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(directory, "release-manifest.json"), append(encoded, '\n'), 0o600); err != nil {
		return err
	}
	names, err := releaseDirectoryFileNames(directory)
	if err != nil {
		return err
	}
	return createReleaseZip(directory, names)
}

func createReleaseZip(directory string, names []string) error {
	zipPath := directory + ".zip"
	output, err := os.OpenFile(zipPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	archive := zip.NewWriter(output)
	for _, name := range names {
		cleanName := filepath.Clean(filepath.FromSlash(name))
		if cleanName == "." || filepath.IsAbs(cleanName) || cleanName == ".." || strings.HasPrefix(cleanName, ".."+string(filepath.Separator)) {
			_ = archive.Close()
			_ = output.Close()
			return fmt.Errorf("invalid release file path %q", name)
		}
		source, err := os.Open(filepath.Join(directory, cleanName))
		if err != nil {
			_ = archive.Close()
			_ = output.Close()
			return err
		}
		entry, err := archive.Create(filepath.ToSlash(cleanName))
		if err == nil {
			_, err = io.Copy(entry, source)
		}
		_ = source.Close()
		if err != nil {
			_ = archive.Close()
			_ = output.Close()
			return err
		}
	}
	return errors.Join(archive.Close(), output.Close())
}

func releaseDirectoryFileNames(directory string) ([]string, error) {
	var names []string
	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == directory || entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("release contains unsupported file %q", path)
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		cleanRelative := filepath.Clean(relative)
		if cleanRelative == "." || cleanRelative == ".." || strings.HasPrefix(cleanRelative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("release file escapes output directory %q", path)
		}
		names = append(names, filepath.ToSlash(cleanRelative))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}

func runCommand(name string, arguments ...string) error {
	command := exec.Command(name, arguments...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=1")
	if err := command.Run(); err != nil {
		return fmt.Errorf("run %s: %w", name, err)
	}
	return nil
}

func componentFileNames() []string {
	names := make([]string, 0, len(componentCommands))
	for _, component := range componentCommands {
		names = append(names, component.fileName)
	}
	return names
}

func releaseFileNames() []string {
	names := append(componentFileNames(), releaseAssetFileNames()...)
	return append(names, "release-manifest.json")
}

func releaseAssetFileNames() []string {
	return []string{applicationIconFileName, thirdPartyNoticesFileName}
}

func releaseAssetSourcePath(name string) string {
	if name == applicationIconFileName {
		return filepath.Join("..", "assets", name)
	}
	return filepath.Join("..", name)
}

func validReleaseVersion(version string) bool {
	parts := strings.Split(strings.TrimSpace(version), ".")
	if len(parts) < 2 || len(parts) > 4 {
		return false
	}
	for _, part := range parts {
		if part == "" || strings.Trim(part, "0123456789") != "" {
			return false
		}
	}
	return runtime.GOOS == "windows"
}
