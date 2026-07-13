package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func assertAfterSwapDeleteSuccess(t *testing.T, rec *httptest.ResponseRecorder, event, message string) {
	t.Helper()
	header := rec.Header().Get("HX-Trigger-After-Swap")
	if header == "" {
		t.Fatal("successful HTMX delete did not set HX-Trigger-After-Swap")
	}
	for i := 0; i < len(header); i++ {
		if header[i] > 0x7f {
			t.Fatalf("HX-Trigger-After-Swap is not transport-safe ASCII: %q", header)
		}
	}
	var events map[string]struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(header), &events); err != nil {
		t.Fatalf("HX-Trigger-After-Swap is invalid JSON: %q: %v", header, err)
	}
	if len(events) != 1 || events[event].Message != message {
		t.Fatalf("success events = %#v, want only %q with message %q", events, event, message)
	}
}
