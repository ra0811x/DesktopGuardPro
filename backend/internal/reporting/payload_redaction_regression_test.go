package reporting

import (
	"bytes"
	"testing"
)

func TestRegressionPayloadRedactsForegroundProcessImage(t *testing.T) {
	payload := []byte(`{"processImage":"C:\\Users\\Alice\\PrivateTools\\app.exe","windowTitle":"secret"}`)
	result, err := redactPayload(payload, RedactionPolicy{ObjectDetails: ObjectDetailBasename, IncludePayload: true, MaximumTextLength: 512})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(result, []byte("Alice")) || bytes.Contains(result, []byte("PrivateTools")) {
		t.Fatalf("basename policy leaks full foreground path: %s", result)
	}
}

func TestForegroundProcessImageRedactionModes(t *testing.T) {
	payload := []byte(`{"nested":[{"processImage":"C:\\Users\\Alice\\app.exe"}]}`)
	for _, mode := range []ObjectDetailMode{ObjectDetailOmit, ObjectDetailBasename, ObjectDetailFull} {
		result, err := redactPayload(payload, RedactionPolicy{ObjectDetails: mode, IncludePayload: true, MaximumTextLength: 512})
		if err != nil {
			t.Fatal(err)
		}
		if mode != ObjectDetailFull && bytes.Contains(result, []byte("Alice")) {
			t.Fatalf("%s leaked path: %s", mode, result)
		}
		if mode == ObjectDetailOmit && bytes.Contains(result, []byte("app.exe")) {
			t.Fatalf("omit leaked basename: %s", result)
		}
		if mode == ObjectDetailFull && !bytes.Contains(result, []byte("Alice")) {
			t.Fatalf("full path lost: %s", result)
		}
	}
}
