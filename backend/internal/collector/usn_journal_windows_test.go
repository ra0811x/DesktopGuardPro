package collector

import (
	"encoding/binary"
	"path/filepath"
	"testing"
	"unicode/utf16"
)

func TestNormalizeUSNFinalPathAndJoinFileName(t *testing.T) {
	parent, err := normalizeUSNFinalPath(`\\?\C:\Evidence\Nested`)
	if err != nil {
		t.Fatalf("normalizeUSNFinalPath() error = %v", err)
	}
	path, err := joinUSNResolvedPath(parent, "report.docx")
	if err != nil {
		t.Fatalf("joinUSNResolvedPath() error = %v", err)
	}
	if path != filepath.Clean(`C:\Evidence\Nested\report.docx`) {
		t.Fatalf("resolved USN path = %q", path)
	}
	for _, invalid := range []string{"", `..\escape.txt`, `nested\file.txt`, `C:\absolute.txt`} {
		if _, err := joinUSNResolvedPath(parent, invalid); err == nil {
			t.Errorf("joinUSNResolvedPath() accepted %q", invalid)
		}
	}
}

func TestParseUSNJournalStateReadsJournalAndNextUSN(t *testing.T) {
	buffer := make([]byte, usnJournalDataV0Size)
	binary.LittleEndian.PutUint64(buffer[0:8], 101)
	binary.LittleEndian.PutUint64(buffer[16:24], 202)
	state, err := parseUSNJournalState(`C:\`, buffer)
	if err != nil {
		t.Fatalf("parseUSNJournalState() error = %v", err)
	}
	if state.Volume != `C:\` || state.JournalID != 101 || state.NextUSN != 202 {
		t.Fatalf("state = %#v", state)
	}
}

func TestParseUSNJournalReadBufferExtractsVersionTwoChange(t *testing.T) {
	buffer := make([]byte, 8)
	binary.LittleEndian.PutUint64(buffer, 900)
	name := utf16.Encode([]rune("report.docx"))
	record := make([]byte, usnRecordV2HeaderSize+len(name)*2)
	binary.LittleEndian.PutUint32(record[0:4], uint32(len(record)))
	binary.LittleEndian.PutUint16(record[4:6], 2)
	binary.LittleEndian.PutUint64(record[8:16], 101)
	binary.LittleEndian.PutUint64(record[16:24], 22)
	binary.LittleEndian.PutUint64(record[24:32], 800)
	binary.LittleEndian.PutUint32(record[40:44], USNReasonDataOverwrite|USNReasonRenameNewName)
	binary.LittleEndian.PutUint16(record[56:58], uint16(len(name)*2))
	binary.LittleEndian.PutUint16(record[58:60], usnRecordV2HeaderSize)
	for index, unit := range name {
		binary.LittleEndian.PutUint16(record[usnRecordV2HeaderSize+index*2:], unit)
	}
	buffer = append(buffer, record...)

	nextUSN, changes, err := parseUSNJournalReadBuffer(`C:\`, buffer)
	if err != nil {
		t.Fatalf("parseUSNJournalReadBuffer() error = %v", err)
	}
	if nextUSN != 900 || len(changes) != 1 {
		t.Fatalf("USN result next=%d changes=%#v", nextUSN, changes)
	}
	change := changes[0]
	if change.Volume != `C:\` || change.FileReference != 101 || change.ParentReference != 22 ||
		change.USN != 800 || change.FileName != "report.docx" || change.Reason&USNReasonRenameNewName == 0 {
		t.Fatalf("USN change = %#v", change)
	}
}

func TestWindowsQueriesUSNJournalForLocalVolume(t *testing.T) {
	volume := filepath.VolumeName(t.TempDir()) + `\`
	state, err := queryUSNJournalState(volume)
	if err != nil {
		t.Skipf("USN journal query is unavailable on this test host: %v", err)
	}
	if state.JournalID == 0 || state.NextUSN == 0 {
		t.Fatalf("state = %#v", state)
	}
}
