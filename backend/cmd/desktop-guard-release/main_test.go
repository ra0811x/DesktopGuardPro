package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"desktopguardpro/internal/maintenance"
)

func TestReleaseInputsExistFromBackendModule(t *testing.T) {
	t.Chdir(filepath.Join("..", ".."))
	for _, name := range releaseAssetFileNames() {
		if data, err := os.ReadFile(releaseAssetSourcePath(name)); err != nil || len(data) == 0 {
			t.Fatalf("release asset %s: size=%d error=%v", name, len(data), err)
		}
	}
	for _, component := range componentCommands {
		if component.nativeProject {
			project := filepath.Join(component.packagePath, "DesktopGuardPro.Native.csproj")
			if _, err := os.Stat(project); err != nil {
				t.Fatalf("native release project %s: %v", project, err)
			}
		}
	}
}

func TestApplicationIconContainsTransparentWindowsSizes(t *testing.T) {
	encoded, err := os.ReadFile(filepath.Join("..", "..", "..", "assets", applicationIconFileName))
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) < 6 || binary.LittleEndian.Uint16(encoded[0:2]) != 0 || binary.LittleEndian.Uint16(encoded[2:4]) != 1 {
		t.Fatal("application icon does not have a valid ICO header")
	}

	count := int(binary.LittleEndian.Uint16(encoded[4:6]))
	wantSizes := []int{16, 24, 32, 48, 64, 128, 256}
	if count != len(wantSizes) {
		t.Fatalf("application icon contains %d layers; want %d", count, len(wantSizes))
	}
	for index, wantSize := range wantSizes {
		entryOffset := 6 + index*16
		if entryOffset+16 > len(encoded) {
			t.Fatalf("application icon directory entry %d is truncated", index)
		}
		entry := encoded[entryOffset : entryOffset+16]
		width := int(entry[0])
		height := int(entry[1])
		if width == 0 {
			width = 256
		}
		if height == 0 {
			height = 256
		}
		if width != wantSize || height != wantSize || binary.LittleEndian.Uint16(entry[6:8]) != 32 {
			t.Fatalf("application icon layer %d = %dx%d/%d-bit; want %dx%d/32-bit", index, width, height, binary.LittleEndian.Uint16(entry[6:8]), wantSize, wantSize)
		}

		imageSize := int(binary.LittleEndian.Uint32(entry[8:12]))
		imageOffset := int(binary.LittleEndian.Uint32(entry[12:16]))
		if imageOffset < 0 || imageSize < 1 || imageOffset+imageSize > len(encoded) {
			t.Fatalf("application icon layer %d points outside the ICO file", index)
		}
		layer, err := png.Decode(bytes.NewReader(encoded[imageOffset : imageOffset+imageSize]))
		if err != nil {
			t.Fatalf("application icon layer %d is not PNG-compressed: %v", index, err)
		}
		_, _, _, alpha := layer.At(layer.Bounds().Min.X, layer.Bounds().Min.Y).RGBA()
		if alpha != 0 {
			t.Fatalf("application icon layer %d does not preserve a transparent corner", index)
		}
		bounds := layer.Bounds()
		for coordinate := 0; coordinate < wantSize; coordinate++ {
			edgePixels := [][2]int{
				{bounds.Min.X + coordinate, bounds.Min.Y},
				{bounds.Min.X + coordinate, bounds.Max.Y - 1},
				{bounds.Min.X, bounds.Min.Y + coordinate},
				{bounds.Max.X - 1, bounds.Min.Y + coordinate},
			}
			for _, pixel := range edgePixels {
				_, _, _, edgeAlpha := layer.At(pixel[0], pixel[1]).RGBA()
				if edgeAlpha != 0 {
					t.Fatalf("application icon layer %d has visible pixels on the canvas edge at %d,%d", index, pixel[0], pixel[1])
				}
			}
		}
	}
}

func TestInstallerLaunchedMaintenanceUsesWindowsGUISubsystem(t *testing.T) {
	for _, component := range componentCommands {
		if component.name != "maintenance" {
			continue
		}
		if !component.windowGUI {
			t.Fatal("maintenance executable must use the Windows GUI subsystem so MSI custom actions do not open a console window")
		}
		return
	}
	t.Fatal("maintenance release component is missing")
}

func TestReleaseBuildUsesNativeWinUIProject(t *testing.T) {
	for _, component := range componentCommands {
		if component.name != "ui" {
			continue
		}
		if !component.nativeProject || component.packagePath != "../frontend/DesktopGuardPro.Native" || component.fileName != "desktop-guard-ui.exe" {
			t.Fatalf("UI release component = %+v", component)
		}
		return
	}
	t.Fatal("UI release component is missing")
}

func TestCreateReleaseManifestRecordsUnsignedComponents(t *testing.T) {
	directory := t.TempDir()
	for _, component := range componentCommands {
		if err := os.WriteFile(filepath.Join(directory, component.fileName), []byte(component.name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(directory, applicationIconFileName), []byte("icon"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, thirdPartyNoticesFileName), []byte("licenses"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := createReleaseManifest(directory, "1.0.0", time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("createReleaseManifest() error = %v", err)
	}
	if manifest.SchemaVersion != 2 || !manifest.SigningRequired || manifest.Target != "windows/amd64" || len(manifest.Components) != 4 {
		t.Fatalf("release manifest = %#v", manifest)
	}
	want := sha256.Sum256([]byte("service"))
	if manifest.Components[0].SHA256 != hex.EncodeToString(want[:]) || manifest.Components[0].Signature != "pending" {
		t.Fatalf("component = %#v", manifest.Components[0])
	}
	if len(manifest.Assets) != 2 || manifest.Assets[0].FileName != applicationIconFileName || manifest.Assets[1].FileName != thirdPartyNoticesFileName {
		t.Fatalf("assets = %#v", manifest.Assets)
	}
}

func TestReleaseManifestIncludesNativeRuntimeAndNestedResources(t *testing.T) {
	directory := t.TempDir()
	for _, component := range componentCommands {
		if err := os.WriteFile(filepath.Join(directory, component.fileName), []byte(component.name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{applicationIconFileName: "icon", thirdPartyNoticesFileName: "licenses", "desktop-guard-ui.dll": "native assembly", "Assets/theme.xaml": "native theme"}
	for name, content := range files {
		path := filepath.Join(directory, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	manifest, err := createReleaseManifest(directory, "2.11.41", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.RuntimeFiles) != len(files) {
		t.Fatalf("incomplete runtime inventory: %+v", manifest.RuntimeFiles)
	}
	for _, file := range manifest.RuntimeFiles {
		content, exists := files[file.Path]
		digest := sha256.Sum256([]byte(content))
		if !exists || file.Size != int64(len(content)) || file.SHA256 != hex.EncodeToString(digest[:]) {
			t.Fatalf("wrong runtime digest: %+v", file)
		}
	}
}

func TestCreateReleaseZipContainsAllFiles(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "release")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	names := releaseFileNames()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := createReleaseZip(directory, names); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(directory + ".zip"); err != nil || info.Size() < 1 {
		t.Fatalf("release zip: %v %#v", err, info)
	}
	archive, err := zip.OpenReader(directory + ".zip")
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	entries := make(map[string]bool, len(archive.File))
	for _, entry := range archive.File {
		entries[entry.Name] = true
	}
	for _, name := range names {
		if !entries[name] {
			t.Fatalf("release zip is missing %q", name)
		}
	}
}

func TestFinalizeReleaseRequiresOneTrustedSigner(t *testing.T) {
	directory := t.TempDir()
	for _, component := range componentCommands {
		if err := os.WriteFile(filepath.Join(directory, component.fileName), []byte(component.name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(directory, applicationIconFileName), []byte("icon"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, thirdPartyNoticesFileName), []byte("licenses"), 0o600); err != nil {
		t.Fatal(err)
	}
	signer := maintenance.SignatureIdentity{SHA256: strings.Repeat("a", 64), Subject: "Desktop Guard Pro Test"}
	if err := finalizeRelease(directory, "1.0.0", time.Now().UTC(), func(string) (maintenance.SignatureIdentity, error) {
		return signer, nil
	}); err != nil {
		t.Fatalf("finalizeRelease() error = %v", err)
	}
	encoded, err := os.ReadFile(filepath.Join(directory, "release-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest releaseManifest
	if err := json.Unmarshal(encoded, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SigningRequired || manifest.SignerSHA256 != signer.SHA256 || manifest.Components[0].Signature != "trusted" {
		t.Fatalf("finalized manifest = %#v", manifest)
	}
}
