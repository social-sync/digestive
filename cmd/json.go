package cmd

import (
	"encoding/json"
	"errors"
	"os"
)

// jsonSchemaVersion is the version of the --json envelope shape. Bump it only
// on a breaking change to the structure, so a machine consumer can guard.
const jsonSchemaVersion = 1

// jsonOutput is the persistent --json flag: when set, a command disables its
// TUI and human output and instead writes exactly one JSON envelope to stdout,
// on success and on failure alike. Diagnostic logging on stderr is suppressed
// unless --log-level was raised explicitly (see newLogger).
var jsonOutput bool

// logLevelChanged records whether --log-level was set explicitly on the command
// line. Under --json, logging is quiet by default; raising --log-level opts back
// into stderr diagnostics while stdout stays pure JSON. Set in root's
// PersistentPreRun, once flags are parsed.
var logLevelChanged bool

// errJSONReported is returned by a command whose outcome (including its error)
// has already been emitted as a JSON envelope on stdout. Execute recognises it
// and exits non-zero without printing anything further to stderr, so the JSON
// contract is never violated by a trailing "error: ..." line.
var errJSONReported = errors.New("json reported")

// jsonEnvelope is the top-level shape every command emits under --json. The
// per-command payload lives in Result; everything around it is stable so a
// consumer has a single parse path.
type jsonEnvelope struct {
	SchemaVersion int      `json:"schema_version"`
	Command       string   `json:"command"`
	Status        string   `json:"status"` // "ok" or "error"
	Error         *string  `json:"error"`
	Warnings      []string `json:"warnings"`
	Result        any      `json:"result"`
}

// emitJSON writes one pretty-printed envelope (trailing newline) to stdout. On
// error, status is "error", the message goes in error, and result is forced to
// null; on success, status is "ok" and error is null.
func emitJSON(command string, result any, warnings []string, err error) {
	if warnings == nil {
		warnings = []string{}
	}
	env := jsonEnvelope{
		SchemaVersion: jsonSchemaVersion,
		Command:       command,
		Warnings:      warnings,
		Result:        result,
	}
	if err != nil {
		env.Status = "error"
		msg := err.Error()
		env.Error = &msg
		env.Result = nil
	} else {
		env.Status = "ok"
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	// A stdout write failure is unrecoverable here and has nowhere to go.
	_ = enc.Encode(env)
}

// reportJSON emits the envelope and returns the error cobra should propagate:
// errJSONReported when err is non-nil (so Execute exits non-zero without a
// stderr line), or nil on success.
func reportJSON(command string, result any, warnings []string, err error) error {
	emitJSON(command, result, warnings, err)
	if err != nil {
		return errJSONReported
	}
	return nil
}

// finish reports a command's outcome. Under --json it emits the envelope; in
// human mode it returns err as-is on failure, or runs the human printer on
// success. The human printer is only called on success, so it may reference a
// result value that is only populated then.
func finish(command string, result any, warnings []string, err error, human func()) error {
	if jsonOutput {
		return reportJSON(command, result, warnings, err)
	}
	if err != nil {
		return err
	}
	if human != nil {
		human()
	}
	return nil
}
