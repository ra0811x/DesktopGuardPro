package collector

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"desktopguardpro/internal/maintenance"

	"golang.org/x/sys/windows"
)

const maximumProcessPathCharacters = 32_768

var processImageTrustCache = struct {
	sync.Mutex
	entries map[string]bool
}{entries: make(map[string]bool)}

var processImageMetadataCache = struct {
	sync.Mutex
	entries map[string]processImageMetadata
}{entries: make(map[string]processImageMetadata)}

type processImageMetadata struct {
	sha256          string
	publisher       string
	signatureStatus string
}

func snapshotWindowsProcesses() ([]ProcessInfo, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("create process snapshot: %w", err)
	}
	defer windows.CloseHandle(snapshot)

	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err := windows.Process32First(snapshot, &entry); err != nil {
		if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
			return nil, nil
		}
		return nil, fmt.Errorf("read first process snapshot entry: %w", err)
	}

	processes := make([]ProcessInfo, 0, 256)
	for {
		createdUTC, imagePath := queryProcessMetadata(entry.ProcessID)
		userSID, username := queryProcessIdentity(entry.ProcessID)
		processes = append(processes, ProcessInfo{
			PID:        entry.ProcessID,
			ParentPID:  entry.ParentProcessID,
			CreatedUTC: createdUTC,
			ImageName:  windows.UTF16ToString(entry.ExeFile[:]),
			ImagePath:  imagePath,
			UserSID:    userSID,
			Username:   username,
		})

		entry.Size = uint32(unsafe.Sizeof(entry))
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				break
			}
			return nil, fmt.Errorf("read next process snapshot entry: %w", err)
		}
	}
	populateParentChains(processes)
	return processes, nil
}

func queryProcessIdentity(processID uint32) (string, string) {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, processID)
	if err != nil {
		return "", ""
	}
	defer windows.CloseHandle(process)
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return "", ""
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil || user.User.Sid == nil {
		return "", ""
	}
	sid := user.User.Sid.String()
	account, domainName, _, err := user.User.Sid.LookupAccount("")
	if err != nil {
		return sid, ""
	}
	if domainName != "" {
		account = domainName + `\` + account
	}
	return sid, account
}

func populateParentChains(processes []ProcessInfo) {
	byPID := make(map[uint32]ProcessInfo, len(processes))
	for _, process := range processes {
		byPID[process.PID] = process
	}
	for index := range processes {
		chain := make([]string, 0, 4)
		parentPID := processes[index].ParentPID
		seen := map[uint32]struct{}{processes[index].PID: {}}
		for depth := 0; parentPID != 0 && depth < 16; depth++ {
			if _, exists := seen[parentPID]; exists {
				break
			}
			seen[parentPID] = struct{}{}
			parent, exists := byPID[parentPID]
			if !exists {
				chain = append(chain, strconv.FormatUint(uint64(parentPID), 10))
				break
			}
			chain = append(chain, fmt.Sprintf("%d:%s", parent.PID, parent.ImageName))
			parentPID = parent.ParentPID
		}
		processes[index].ParentChain = strings.Join(chain, " -> ")
	}
}

func softwareProcessAction(process ProcessInfo) string {
	name := strings.ToLower(strings.TrimSpace(process.ImageName))
	if name == "" {
		name = strings.ToLower(filepath.Base(process.ImagePath))
	}
	for _, marker := range []string{"msiexec.exe", "setup.exe", "installer.exe", "appinstaller.exe", "winget.exe", "choco.exe"} {
		if name == marker || strings.Contains(name, "setup") || strings.Contains(name, "install") {
			return "software_installer_started"
		}
	}
	path := strings.ToLower(filepath.Clean(process.ImagePath))
	if filepath.Ext(path) == ".exe" && !strings.Contains(path, `\windows\`) &&
		!strings.Contains(path, `\program files\`) && !strings.Contains(path, `\program files (x86)\`) {
		return "portable_program_executed"
	}
	return ""
}

func processImageSignatureTrusted(path string) bool {
	if path == "" {
		return false
	}
	processImageTrustCache.Lock()
	if trusted, found := processImageTrustCache.entries[path]; found {
		processImageTrustCache.Unlock()
		return trusted
	}
	processImageTrustCache.Unlock()
	_, err := maintenance.VerifyAuthenticodeComponent(path)
	trusted := err == nil
	processImageTrustCache.Lock()
	processImageTrustCache.entries[path] = trusted
	processImageTrustCache.Unlock()
	return trusted
}

// inspectProcessImage is reserved for strict file read attribution. The
// periodic process snapshot uses processImageSignatureTrusted so it does not
// calculate or persist image metadata.
func inspectProcessImage(path string) processImageMetadata {
	if path == "" {
		return processImageMetadata{signatureStatus: "unavailable"}
	}
	processImageMetadataCache.Lock()
	if metadata, found := processImageMetadataCache.entries[path]; found {
		processImageMetadataCache.Unlock()
		return metadata
	}
	processImageMetadataCache.Unlock()
	sha256, _ := hashFileContent(path)
	metadata := processImageMetadata{sha256: sha256, signatureStatus: "untrusted"}
	if identity, err := maintenance.VerifyAuthenticodeComponent(path); err == nil {
		metadata.publisher = identity.Subject
		metadata.signatureStatus = "trusted"
	}
	processImageMetadataCache.Lock()
	processImageMetadataCache.entries[path] = metadata
	processImageMetadataCache.Unlock()
	return metadata
}

func queryProcessMetadata(processID uint32) (time.Time, string) {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, processID)
	if err != nil {
		return time.Time{}, ""
	}
	defer windows.CloseHandle(process)

	var creation, exit, kernel, user windows.Filetime
	var createdUTC time.Time
	if err := windows.GetProcessTimes(process, &creation, &exit, &kernel, &user); err == nil {
		nanoseconds := creation.Nanoseconds()
		if nanoseconds > 0 {
			createdUTC = time.Unix(0, nanoseconds).UTC()
		}
	}

	buffer := make([]uint16, maximumProcessPathCharacters)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(process, 0, &buffer[0], &size); err != nil {
		return createdUTC, ""
	}
	return createdUTC, windows.UTF16ToString(buffer[:size])
}
