package logging

import (
	"encoding/json"
	"log"
	"os"
	"strings"
	"testing"
)

// TestSetupJSON verifies that Setup("json") redirects stdlib log output into
// structured JSON on stderr. It mutates global state, so it must not run in
// parallel with other tests.
func TestSetupJSON(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	oldStderr := os.Stderr
	os.Stderr = w
	defer func() {
		os.Stderr = oldStderr
	}()

	Setup("json")
	log.Print("hello json test")

	w.Close()
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	os.Stderr = oldStderr

	out := string(buf[:n])
	if !strings.Contains(out, `"msg":"hello json test"`) {
		t.Fatalf("stderr output missing msg field, got: %s", out)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(buf[:n], &parsed); err != nil {
		t.Fatalf("stderr output is not valid JSON: %v", err)
	}
	if parsed["msg"] != "hello json test" {
		t.Fatalf("msg field = %v, want hello json test", parsed["msg"])
	}
}
