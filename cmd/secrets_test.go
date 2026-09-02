package cmd

import (
	"errors"
	"strings"
	"testing"
)

func TestAWSCredentialHintSSO(t *testing.T) {
	err := errors.New("failed to refresh cached credentials, refresh cached SSO token failed, operation error SSO OIDC: CreateToken, InvalidGrantException")
	got := awsCredentialHint(err, "smallest")
	if !strings.Contains(got, "aws sso login --profile smallest") {
		t.Fatalf("expected login hint, got: %s", got)
	}
	if !strings.Contains(got, "aws CLI may still work") {
		t.Fatalf("expected CLI-cache caveat, got: %s", got)
	}
}

func TestAWSCredentialHintOther(t *testing.T) {
	err := errors.New("no EC2 IMDS role found")
	got := awsCredentialHint(err, "default")
	if strings.Contains(got, "sso login") {
		t.Fatalf("non-SSO error must not suggest sso login: %s", got)
	}
	if !strings.Contains(got, "no EC2 IMDS role found") {
		t.Fatalf("expected original error, got: %s", got)
	}
}

func TestAWSProfileName(t *testing.T) {
	t.Setenv("AWS_PROFILE", "envprof")
	if got := awsProfileName("spec"); got != "spec" {
		t.Fatalf("spec override: got %q", got)
	}
	if got := awsProfileName(""); got != "envprof" {
		t.Fatalf("env fallback: got %q", got)
	}
	t.Setenv("AWS_PROFILE", "")
	if got := awsProfileName(""); got != "default" {
		t.Fatalf("default: got %q", got)
	}
}
