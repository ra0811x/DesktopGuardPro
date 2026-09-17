package maintenance

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

func verifyMSIReleaseFiles(directory, version, signer string) error {
	encoded, err := readOptionalMSIFile(filepath.Join(directory, "release-manifest.json"))
	if err != nil {
		return err
	}
	var release struct {
		ProductVersion  string `json:"productVersion"`
		SigningRequired bool   `json:"signingRequired"`
		SignerSHA256    string `json:"signerSha256"`
		Components      []struct {
			Name      string `json:"name"`
			FileName  string `json:"fileName"`
			Size      int64  `json:"size"`
			SHA256    string `json:"sha256"`
			Signature string `json:"signature"`
		} `json:"components"`
	}
	if err := json.Unmarshal(encoded, &release); err != nil {
		return err
	}
	if release.ProductVersion != version || release.SigningRequired || !strings.EqualFold(release.SignerSHA256, signer) || len(release.Components) != 4 {
		return ErrInstallManifestInvalid
	}
	remaining := map[string]bool{"service": true, "ui": true, "agent": true, "maintenance": true}
	for _, component := range release.Components {
		if !remaining[component.Name] || component.FileName != "desktop-guard-"+component.Name+".exe" || component.Signature != "trusted" {
			return ErrInstallManifestInvalid
		}
		delete(remaining, component.Name)
		actual, err := inspectInstallComponent(component.Name, filepath.Join(directory, component.FileName))
		if err != nil {
			return err
		}
		if actual.Size != component.Size || !strings.EqualFold(actual.SHA256, component.SHA256) {
			return fmt.Errorf("MSI component %s does not match the signed release; close the installed applications and retry", component.Name)
		}
	}
	return nil
}
