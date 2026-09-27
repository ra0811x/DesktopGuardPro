package collector

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf16"
	"unsafe"

	"desktopguardpro/internal/domain"

	"golang.org/x/sys/windows"
)

const (
	fsctlQueryUSNJournal  = 0x000900f4
	fsctlReadUSNJournal   = 0x000900bb
	usnJournalDataV0Size  = 56
	usnReadInputV0Size    = 40
	usnRecordV2HeaderSize = 60
	usnReadBufferSize     = 64 * 1024
	maximumUSNChanges     = 100_000
)

var openFileByID = windows.NewLazySystemDLL("kernel32.dll").NewProc("OpenFileById")

type windowsFileIDDescriptor struct {
	Size   uint32
	Type   uint32
	FileID [16]byte
}

type USNJournalState struct {
	Volume    string
	JournalID uint64
	NextUSN   uint64
}

type USNJournalChange struct {
	Volume          string
	FileReference   uint64
	ParentReference uint64
	USN             uint64
	Reason          uint32
	FileName        string
}

const (
	USNReasonDataOverwrite  = 0x00000001
	USNReasonDataExtend     = 0x00000002
	USNReasonDataTruncation = 0x00000004
	USNReasonFileCreate     = 0x00000100
	USNReasonFileDelete     = 0x00000200
	USNReasonRenameOldName  = 0x00001000
	USNReasonRenameNewName  = 0x00002000
)

func QueryUSNJournalStates(targets []domain.MonitoringTarget) ([]USNJournalState, error) {
	volumes := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		volume := strings.ToUpper(filepath.VolumeName(target.Path))
		if volume == "" {
			continue
		}
		volumes[volume+`\`] = struct{}{}
	}
	states := make([]USNJournalState, 0, len(volumes))
	for volume := range volumes {
		state, err := queryUSNJournalState(volume)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	sort.Slice(states, func(left, right int) bool { return states[left].Volume < states[right].Volume })
	return states, nil
}

func queryUSNJournalState(volume string) (USNJournalState, error) {
	handle, volume, err := openUSNVolume(volume)
	if err != nil {
		return USNJournalState{}, err
	}
	defer windows.CloseHandle(handle)
	buffer := make([]byte, usnJournalDataV0Size)
	var returned uint32
	if err := windows.DeviceIoControl(handle, fsctlQueryUSNJournal, nil, 0, &buffer[0], uint32(len(buffer)), &returned, nil); err != nil {
		return USNJournalState{}, fmt.Errorf("query USN journal for %s: %w", volume, err)
	}
	if int(returned) < usnJournalDataV0Size {
		return USNJournalState{}, errors.New("USN journal query returned an incomplete response")
	}
	return parseUSNJournalState(volume, buffer)
}

func ReadUSNJournalChanges(ctx context.Context, volume string, journalID, startUSN, endUSN uint64) ([]USNJournalChange, error) {
	if ctx == nil || journalID == 0 || startUSN >= endUSN {
		return nil, errors.New("USN journal read range is invalid")
	}
	handle, volume, err := openUSNVolume(volume)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(handle)
	changes := make([]USNJournalChange, 0)
	nextUSN := startUSN
	for nextUSN < endUSN {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		input := makeUSNReadInput(journalID, nextUSN)
		output := make([]byte, usnReadBufferSize)
		var returned uint32
		err := windows.DeviceIoControl(handle, fsctlReadUSNJournal, &input[0], uint32(len(input)), &output[0], uint32(len(output)), &returned, nil)
		if err != nil {
			if errors.Is(err, windows.ERROR_HANDLE_EOF) {
				break
			}
			return nil, fmt.Errorf("read USN journal for %s: %w", volume, err)
		}
		if returned < 8 {
			return nil, errors.New("USN journal read returned an incomplete response")
		}
		readNextUSN, batch, err := parseUSNJournalReadBuffer(volume, output[:returned])
		if err != nil {
			return nil, err
		}
		for _, change := range batch {
			if change.USN >= endUSN {
				continue
			}
			changes = append(changes, change)
			if len(changes) > maximumUSNChanges {
				return nil, errors.New("USN journal change count exceeds 100000; use file baseline reconciliation")
			}
		}
		if readNextUSN <= nextUSN {
			return nil, errors.New("USN journal read did not advance")
		}
		nextUSN = readNextUSN
	}
	return changes, nil
}

func ResolveUSNChangePath(change USNJournalChange) (string, error) {
	if change.ParentReference == 0 {
		return "", errors.New("USN parent file reference is missing")
	}
	volumeHandle, volume, err := openUSNVolume(change.Volume)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(volumeHandle)
	descriptor := windowsFileIDDescriptor{Size: uint32(unsafe.Sizeof(windowsFileIDDescriptor{}))}
	binary.LittleEndian.PutUint64(descriptor.FileID[:8], change.ParentReference)
	parentValue, _, callErr := openFileByID.Call(
		uintptr(volumeHandle), uintptr(unsafe.Pointer(&descriptor)), uintptr(windows.FILE_READ_ATTRIBUTES),
		uintptr(windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE), 0,
		uintptr(windows.FILE_FLAG_BACKUP_SEMANTICS),
	)
	parentHandle := windows.Handle(parentValue)
	if parentHandle == windows.InvalidHandle {
		return "", fmt.Errorf("open USN parent %d on %s: %w", change.ParentReference, volume, callErr)
	}
	defer windows.CloseHandle(parentHandle)
	buffer := make([]uint16, 32_768)
	length, err := windows.GetFinalPathNameByHandle(parentHandle, &buffer[0], uint32(len(buffer)), 0)
	if err != nil {
		return "", fmt.Errorf("resolve USN parent %d on %s: %w", change.ParentReference, volume, err)
	}
	if length == 0 || length >= uint32(len(buffer)) {
		return "", errors.New("resolved USN parent path is invalid")
	}
	parent, err := normalizeUSNFinalPath(windows.UTF16ToString(buffer[:length]))
	if err != nil {
		return "", err
	}
	return joinUSNResolvedPath(parent, change.FileName)
}

func normalizeUSNFinalPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if strings.HasPrefix(path, `\\?\UNC\`) {
		path = `\\` + strings.TrimPrefix(path, `\\?\UNC\`)
	} else {
		path = strings.TrimPrefix(path, `\\?\`)
	}
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) || filepath.VolumeName(path) == "" {
		return "", errors.New("resolved USN parent path is not absolute")
	}
	return path, nil
}

func joinUSNResolvedPath(parent, fileName string) (string, error) {
	fileName = strings.TrimSpace(fileName)
	if fileName == "" || fileName == "." || fileName == ".." || filepath.Base(fileName) != fileName || filepath.IsAbs(fileName) {
		return "", errors.New("USN change file name is invalid")
	}
	return filepath.Join(parent, fileName), nil
}

func openUSNVolume(volume string) (windows.Handle, string, error) {
	volume = strings.ToUpper(strings.TrimSpace(volume))
	if len(volume) != 3 || volume[1:] != `:\` {
		return windows.InvalidHandle, "", errors.New("USN journal volume must be a local drive root")
	}
	devicePath := `\\.\` + volume[:2]
	pointer, err := windows.UTF16PtrFromString(devicePath)
	if err != nil {
		return windows.InvalidHandle, "", fmt.Errorf("encode USN volume path: %w", err)
	}
	handle, err := windows.CreateFile(pointer, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return windows.InvalidHandle, "", fmt.Errorf("open USN volume %s: %w", volume, err)
	}
	return handle, volume, nil
}

func parseUSNJournalState(volume string, buffer []byte) (USNJournalState, error) {
	if len(buffer) < usnJournalDataV0Size {
		return USNJournalState{}, errors.New("USN journal data is truncated")
	}
	state := USNJournalState{
		Volume: strings.ToUpper(strings.TrimSpace(volume)), JournalID: binary.LittleEndian.Uint64(buffer[0:8]),
		NextUSN: binary.LittleEndian.Uint64(buffer[16:24]),
	}
	if state.Volume == "" || state.JournalID == 0 {
		return USNJournalState{}, errors.New("USN journal state is invalid")
	}
	return state, nil
}

func makeUSNReadInput(journalID, startUSN uint64) []byte {
	input := make([]byte, usnReadInputV0Size)
	binary.LittleEndian.PutUint64(input[0:8], startUSN)
	binary.LittleEndian.PutUint32(input[8:12], 0xffffffff)
	binary.LittleEndian.PutUint64(input[32:40], journalID)
	return input
}

func parseUSNJournalReadBuffer(volume string, buffer []byte) (uint64, []USNJournalChange, error) {
	if len(buffer) < 8 {
		return 0, nil, errors.New("USN journal read buffer is truncated")
	}
	nextUSN := binary.LittleEndian.Uint64(buffer[0:8])
	changes := []USNJournalChange{}
	for offset := 8; offset < len(buffer); {
		if len(buffer)-offset < usnRecordV2HeaderSize {
			return 0, nil, errors.New("USN journal record header is truncated")
		}
		recordLength := int(binary.LittleEndian.Uint32(buffer[offset : offset+4]))
		if recordLength < usnRecordV2HeaderSize || recordLength > len(buffer)-offset {
			return 0, nil, errors.New("USN journal record length is invalid")
		}
		majorVersion := binary.LittleEndian.Uint16(buffer[offset+4 : offset+6])
		if majorVersion != 2 {
			return 0, nil, fmt.Errorf("unsupported USN journal record version %d", majorVersion)
		}
		nameLength := int(binary.LittleEndian.Uint16(buffer[offset+56 : offset+58]))
		nameOffset := int(binary.LittleEndian.Uint16(buffer[offset+58 : offset+60]))
		if nameOffset < usnRecordV2HeaderSize || nameLength%2 != 0 || nameOffset+nameLength > recordLength {
			return 0, nil, errors.New("USN journal record file name is invalid")
		}
		nameBytes := buffer[offset+nameOffset : offset+nameOffset+nameLength]
		nameUnits := make([]uint16, len(nameBytes)/2)
		for index := range nameUnits {
			nameUnits[index] = binary.LittleEndian.Uint16(nameBytes[index*2 : index*2+2])
		}
		changes = append(changes, USNJournalChange{
			Volume: strings.ToUpper(strings.TrimSpace(volume)), FileReference: binary.LittleEndian.Uint64(buffer[offset+8 : offset+16]),
			ParentReference: binary.LittleEndian.Uint64(buffer[offset+16 : offset+24]), USN: binary.LittleEndian.Uint64(buffer[offset+24 : offset+32]),
			Reason: binary.LittleEndian.Uint32(buffer[offset+40 : offset+44]), FileName: string(utf16.Decode(nameUnits)),
		})
		offset += recordLength
	}
	return nextUSN, changes, nil
}
