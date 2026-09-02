package cmd

import (
	"strings"
	"testing"

	"github.com/smallest-inc/velocity-cli/internal/velocity"
)

func TestGenerateRoutesYmlStripPrefix(t *testing.T) {
	services := map[string]velocity.Service{
		"dispatcher": {Port: 8081, Routes: []velocity.Route{{Path: "/dispatch/", StripPrefix: true}}},
		"api":        {Port: 4001, Routes: []velocity.Route{{Path: "/api/v1/"}}},
	}
	out := generateRoutesYml(services, "box.example.com", []string{"10.0.0.0/8"})

	for _, want := range []string{
		"    dispatcher-strip:\n      stripPrefix:\n        prefixes:\n          - \"/dispatch/\"\n",
		"    vpn-only:\n      ipAllowList:\n",
		"      middlewares:\n        - vpn-only\n        - dispatcher-strip\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "api-strip") {
		t.Fatalf("route without strip_prefix must not get a strip middleware:\n%s", out)
	}
}

func TestGenerateRoutesYmlNoMiddlewaresSection(t *testing.T) {
	services := map[string]velocity.Service{
		"api": {Port: 4001, Routes: []velocity.Route{{Path: "/api/v1/"}}},
	}
	out := generateRoutesYml(services, "box.example.com", nil)
	if strings.Contains(out, "middlewares") {
		t.Fatalf("no allowlist and no strip_prefix must emit no middlewares:\n%s", out)
	}
}
