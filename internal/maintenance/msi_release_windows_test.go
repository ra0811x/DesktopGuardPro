package maintenance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMSIRejectsOldOrDeferredFilesEvenWhenPublisherMatches(t *testing.T) {
	directory := t.TempDir()
	components := []map[string]any{}
	for _, name := range []string{"service", "ui", "agent", "maintenance"} {
		file := "desktop-guard-" + name + ".exe"
		content := []byte("new signed " + name)
		if err := os.WriteFile(filepath.Join(directory, file), content, 0600); err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(content)
		components = append(components, map[string]any{"name": name, "fileName": file, "size": len(content), "sha256": hex.EncodeToString(hash[:]), "signature": "trusted"})
	}
	signer := strings.Repeat("a", 64)
	manifest := map[string]any{"productVersion": "1.2.3", "signingRequired": false, "signerSha256": signer, "components": components}
	encoded, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(directory, "release-manifest.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyMSIReleaseFiles(directory, "1.2.3", signer); err != nil {
		t.Fatal(err)
	}
	if err := verifyMSIReleaseFiles(directory, "1.2.4", signer); err == nil {
		t.Fatal("wrong product version accepted")
	}
	if err := os.WriteFile(filepath.Join(directory, "desktop-guard-ui.exe"), []byte("old signed UI still locked"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyMSIReleaseFiles(directory, "1.2.3", signer); err == nil {
		t.Fatal("MSI committed with a deferred old component")
	}
}
