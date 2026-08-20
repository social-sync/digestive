package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"
)

// captureStdout runs fn with os.Stdout redirected to a pipe and returns what it
// wrote.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()
	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(out)
}

func TestEmitJSONSuccess(t *testing.T) {
	out := captureStdout(t, func() {
		emitJSON("export", map[string]any{"run_dir": "x"}, nil, nil)
	})

	var env jsonEnvelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if env.SchemaVersion != jsonSchemaVersion {
		t.Errorf("schema_version = %d, want %d", env.SchemaVersion, jsonSchemaVersion)
	}
	if env.Command != "export" || env.Status != "ok" {
		t.Errorf("command/status = %q/%q, want export/ok", env.Command, env.Status)
	}
	if env.Error != nil {
		t.Errorf("error = %v, want nil", *env.Error)
	}
	if env.Warnings == nil {
		t.Error("warnings should be an empty array, not null")
	}
	if env.Result == nil {
		t.Error("result should be present on success")
	}
}

func TestEmitJSONErrorForcesResultNull(t *testing.T) {
	out := captureStdout(t, func() {
		// A non-nil result must still be nulled out on error.
		emitJSON("validate", map[string]any{"tables": []string{"a"}}, []string{"w"}, errors.New("boom"))
	})

	var env jsonEnvelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if env.Status != "error" {
		t.Errorf("status = %q, want error", env.Status)
	}
	if env.Error == nil || *env.Error != "boom" {
		t.Errorf("error = %v, want boom", env.Error)
	}
	if env.Result != nil {
		t.Errorf("result = %v, want null on error", env.Result)
	}
	if len(env.Warnings) != 1 || env.Warnings[0] != "w" {
		t.Errorf("warnings = %v, want [w]", env.Warnings)
	}
}

func TestFinishJSONReportsError(t *testing.T) {
	jsonOutput = true
	defer func() { jsonOutput = false }()

	humanCalled := false
	out := captureStdout(t, func() {
		err := finish("init", nil, nil, errors.New("nope"), func() { humanCalled = true })
		if !errors.Is(err, errJSONReported) {
			t.Errorf("finish returned %v, want errJSONReported", err)
		}
	})
	if humanCalled {
		t.Error("human printer must not run under --json")
	}
	if out == "" {
		t.Error("finish should have emitted a JSON envelope")
	}
}

func TestFinishHumanPathRunsPrinterOnSuccess(t *testing.T) {
	jsonOutput = false

	humanCalled := false
	out := captureStdout(t, func() {
		if err := finish("init", nil, nil, nil, func() { humanCalled = true }); err != nil {
			t.Errorf("finish returned %v, want nil", err)
		}
	})
	if !humanCalled {
		t.Error("human printer should run on success without --json")
	}
	if out != "" {
		t.Errorf("human path should not emit JSON, got %q", out)
	}
}
