// Package audit writes a per-run compliance record for an export or sync. The
// record captures who requested the run, when it happened, the effective
// (secret-redacted) config that governed it, the run's full manifest, and the
// per-table row counts — enough for an auditor to answer "what data left, how
// was it transformed, and who asked for it" without granting access to the data
// itself.
package audit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/social-sync/digestive/internal/config"
	"github.com/social-sync/digestive/internal/manifest"
)

// Version is the audit record schema version.
const Version = 1

// Document is one audit record. All actions (export, sync, and future ones such
// as download) share this schema; Action distinguishes them.
type Document struct {
	AuditVersion int                `json:"audit_version"`
	Action       string             `json:"action"`
	Requester    Requester          `json:"requester"`
	Hostname     string             `json:"hostname"`
	Timestamps   Timestamps         `json:"timestamps"`
	Output       Output             `json:"output"`
	Config       map[string]any     `json:"config"`
	Manifest     *manifest.Manifest `json:"manifest"`
	RowCounts    map[string]int64   `json:"row_counts"`
	ToolVersion  string             `json:"tool_version"`
}

// Requester identifies the person who requested the run.
type Requester struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// Timestamps records when the export ran and when this record was written, both
// RFC3339 UTC.
type Timestamps struct {
	ExportStartedAt string `json:"export_started_at"`
	AuditWrittenAt  string `json:"audit_written_at"`
}

// Output identifies the export artifact this record describes.
type Output struct {
	RunName      string `json:"run_name"`
	RunDirectory string `json:"run_directory"`
}

// BuildInput carries everything needed to assemble a Document.
type BuildInput struct {
	// Action is "export" or "sync".
	Action string
	// Requester is the validated name/email of the person who ran the command.
	Requester Requester
	// Config is the effective config that governed the run; it is redacted here.
	Config *config.Config
	// Manifest is the completed run's manifest.
	Manifest *manifest.Manifest
	// RunName is the run directory's base name.
	RunName string
	// RunDirectory is the (ideally absolute) path to the run directory.
	RunDirectory string
	// ToolVersion is the digestive build version.
	ToolVersion string
	// Now stamps AuditWrittenAt.
	Now time.Time
}

// Build assembles an audit Document, redacting the config and deriving the
// per-table row counts from the manifest. The hostname is read from the OS; a
// lookup failure is non-fatal and recorded as "unknown".
func Build(in BuildInput) (Document, error) {
	if in.Manifest == nil {
		return Document{}, fmt.Errorf("audit: manifest is required")
	}
	redacted, err := in.Config.Redacted()
	if err != nil {
		return Document{}, fmt.Errorf("audit: redact config: %w", err)
	}

	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}

	rowCounts := make(map[string]int64, len(in.Manifest.Tables))
	for _, t := range in.Manifest.Tables {
		rowCounts[t.Name] = t.Rows
	}

	return Document{
		AuditVersion: Version,
		Action:       in.Action,
		Requester:    in.Requester,
		Hostname:     host,
		Timestamps: Timestamps{
			ExportStartedAt: in.Manifest.CreatedAt,
			AuditWrittenAt:  in.Now.UTC().Format(time.RFC3339),
		},
		Output: Output{
			RunName:      in.RunName,
			RunDirectory: in.RunDirectory,
		},
		Config:      redacted,
		Manifest:    in.Manifest,
		RowCounts:   rowCounts,
		ToolVersion: in.ToolVersion,
	}, nil
}

// Write serialises doc and stores it via sink under a collision-resistant
// object name derived from the run name, hostname, and random suffix.
func Write(ctx context.Context, sink Sink, doc Document) error {
	name, err := objectName(doc.Output.RunName, doc.Hostname)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("audit: marshal record: %w", err)
	}
	data = append(data, '\n')
	return sink.Write(ctx, name, data)
}

// objectName builds "<run-name>-<hostname>-<8hex>.json". The random suffix makes
// it collision-resistant when many people or machines write into one shared
// destination; hostname attributes origin.
func objectName(runName, hostname string) (string, error) {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("audit: generate object name: %w", err)
	}
	return fmt.Sprintf("%s-%s-%s.json", sanitize(runName), sanitize(hostname), hex.EncodeToString(buf[:])), nil
}

// sanitize replaces filesystem/object-key-unfriendly characters so the name is
// safe as both a local filename and an S3 key segment.
func sanitize(s string) string {
	if s == "" {
		return "unknown"
	}
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}
