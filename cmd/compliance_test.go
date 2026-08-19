package cmd

import (
	"testing"

	"github.com/social-sync/digestive/internal/config"
)

func TestRequireRequester(t *testing.T) {
	compliant := &config.Config{Compliance: &config.ComplianceConfig{
		Audit: config.AuditConfig{Directory: "./audit"},
	}}
	plain := &config.Config{}

	cases := []struct {
		name    string
		cfg     *config.Config
		reqName string
		email   string
		wantErr bool
	}{
		{"compliance off, no flags ok", plain, "", "", false},
		{"compliance on, both valid", compliant, "Jane", "jane@example.com", false},
		{"compliance on, name trimmed valid", compliant, "  Jane  ", "jane@example.com", false},
		{"compliance on, missing name", compliant, "", "jane@example.com", true},
		{"compliance on, blank name", compliant, "   ", "jane@example.com", true},
		{"compliance on, missing email", compliant, "Jane", "", true},
		{"compliance on, invalid email", compliant, "Jane", "not-an-email", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requesterName = tc.reqName
			requesterEmail = tc.email
			t.Cleanup(func() { requesterName, requesterEmail = "", "" })

			got, err := requireRequester(tc.cfg)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.cfg.Compliance != nil && got.Name == "" {
				t.Errorf("expected populated requester, got %+v", got)
			}
		})
	}
}
