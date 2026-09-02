package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	remotessh "github.com/smallest-inc/velocity-cli/internal/ssh"
	"github.com/smallest-inc/velocity-cli/internal/ui"
	"github.com/smallest-inc/velocity-cli/internal/velocity"
)

// mergeAWSSecrets pulls per-service secrets from AWS Secrets Manager (on the
// laptop, using the developer's local AWS credential chain) and appends any
// keys the box's .env doesn't already have. Idempotent: keys already on the
// box are never overwritten by SM, and re-runs are no-ops once everything
// is in place.
//
// Failure modes are all non-fatal — a warning is printed and the next
// service is processed. Returns nil only when nothing in the config is
// requested; an actual error is returned only for misconfigurations the
// developer can fix locally (e.g. service name doesn't exist in the spec).
func mergeAWSSecrets(ctx *serviceContext, cfg *velocity.AWSSecretsManagerConfig) error {
	if cfg == nil || len(cfg.Services) == 0 {
		return nil
	}

	// AWS SDK config: profile + region. The profile is optional — if
	// unset, the SDK falls back to AWS_PROFILE env var, then default
	// chain (env / ~/.aws/config SSO profile / ~/.aws/credentials / IMDS).
	region := cfg.Region
	if region == "" {
		region = "ap-south-1"
	}
	profile := awsProfileName(cfg.AWSProfile)
	loadOpts := []func(*awscfg.LoadOptions) error{awscfg.WithRegion(region)}
	if cfg.AWSProfile != "" {
		loadOpts = append(loadOpts, awscfg.WithSharedConfigProfile(cfg.AWSProfile))
	}

	loadCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	awsCfg, err := awscfg.LoadDefaultConfig(loadCtx, loadOpts...)
	if err != nil {
		ui.Warn(awsCredentialHint(err, profile))
		return nil
	}
	// Credentials resolve lazily on the first API call. Resolve them here so
	// an expired SSO session is reported once with the fix, rather than as
	// an opaque fetch failure per service.
	if _, err := awsCfg.Credentials.Retrieve(loadCtx); err != nil {
		ui.Warn(awsCredentialHint(err, profile))
		return nil
	}
	sm := secretsmanager.NewFromConfig(awsCfg)

	globalBL := toSet(cfg.GlobalBlocklist)
	remotePath := ctx.spec.Remote.Path

	// Validate that every service mapping references a real service in the spec.
	// Done in one pass up front so a typo is loud rather than a silent miss.
	for _, svcMap := range cfg.Services {
		if _, ok := ctx.spec.Services[svcMap.Name]; !ok {
			ui.Warn(fmt.Sprintf("secrets: service %q in sync.secrets.aws_secrets_manager.services is not declared under services: — skipping", svcMap.Name))
		}
	}

	for _, svcMap := range cfg.Services {
		svc, ok := ctx.spec.Services[svcMap.Name]
		if !ok {
			continue
		}
		envPath := fmt.Sprintf("%s/%s/.env", remotePath, strings.TrimPrefix(svc.Path, "./"))

		// Fetch the SM secret.
		fetchCtx, fetchCancel := context.WithTimeout(context.Background(), 15*time.Second)
		out, err := sm.GetSecretValue(fetchCtx, &secretsmanager.GetSecretValueInput{
			SecretId: aws.String(svcMap.SecretID),
		})
		fetchCancel()
		if err != nil {
			ui.Warn(fmt.Sprintf("secrets: %s (%s): fetch failed: %v", svcMap.Name, svcMap.SecretID, err))
			continue
		}
		if out.SecretString == nil || *out.SecretString == "" {
			ui.Warn(fmt.Sprintf("secrets: %s (%s): empty SecretString — skipping", svcMap.Name, svcMap.SecretID))
			continue
		}

		// Secrets stored as flat JSON objects ({"KEY":"value", …}). Reject
		// anything else — binary or non-object structures aren't injectable
		// as .env lines without more semantics than the SDK gives us.
		var smKV map[string]string
		if err := json.Unmarshal([]byte(*out.SecretString), &smKV); err != nil {
			ui.Warn(fmt.Sprintf("secrets: %s (%s): SecretString isn't a flat JSON object — skipping", svcMap.Name, svcMap.SecretID))
			continue
		}

		// Read the box's current .env to compute additions.
		existing, _ := remotessh.Exec(ctx.keyPath, ctx.user, ctx.addr,
			fmt.Sprintf("cat %s 2>/dev/null || true", envPath))
		existingKeys := parseEnvKeys(existing)
		blocklist := union(globalBL, toSet(svcMap.Blocklist))

		// Sort the additions for stable output / predictable diffs.
		var addKeys []string
		for k := range smKV {
			if _, has := existingKeys[k]; has {
				continue
			}
			if _, blocked := blocklist[k]; blocked {
				continue
			}
			// Defensive: an injected value containing a newline would
			// corrupt the .env file. Skip with a warning rather than
			// emit broken output — the operator can fix the SM secret.
			if strings.ContainsAny(smKV[k], "\n\r") {
				ui.Warn(fmt.Sprintf("secrets: %s: skipping %s (value contains newline)", svcMap.Name, k))
				continue
			}
			addKeys = append(addKeys, k)
		}
		if len(addKeys) == 0 {
			ui.Step(Verbose, fmt.Sprintf("secrets: %s — nothing to append (all keys already present or blocked)", svcMap.Name))
			continue
		}
		sort.Strings(addKeys)

		var addLines []string
		for _, k := range addKeys {
			addLines = append(addLines, fmt.Sprintf("%s=%s", k, smKV[k]))
		}
		merged := strings.TrimRight(existing, "\n") + "\n" + strings.Join(addLines, "\n") + "\n"

		// Heredoc-based write: avoids shell escaping pitfalls in the values.
		writeCmd := fmt.Sprintf("cat > %s <<'VCTL_ENV_EOF'\n%sVCTL_ENV_EOF", envPath, merged)
		if _, err := remotessh.Exec(ctx.keyPath, ctx.user, ctx.addr, writeCmd); err != nil {
			ui.Warn(fmt.Sprintf("secrets: %s: failed to write merged .env: %v", svcMap.Name, err))
			continue
		}
		ui.Step(Verbose, fmt.Sprintf("secrets: %s — appended %d key(s) to %s", svcMap.Name, len(addKeys), envPath))
	}

	return nil
}

// awsProfileName is the profile the SDK resolves: the spec override, then
// AWS_PROFILE, else "default".
func awsProfileName(specProfile string) string {
	if specProfile != "" {
		return specProfile
	}
	if p := os.Getenv("AWS_PROFILE"); p != "" {
		return p
	}
	return "default"
}

// awsCredentialHint turns a credential-resolution error into one actionable
// warning. SSO failures (expired or missing cached token, revoked refresh
// grant) name the exact login command. The aws CLI keeps its own
// role-credential cache and can keep working for hours after the SSO token
// expires, so a passing `aws sts get-caller-identity` does not mean vctl has
// a usable token — the hint says so to avoid that misdiagnosis.
func awsCredentialHint(err error, profile string) string {
	if strings.Contains(strings.ToLower(err.Error()), "sso") {
		return fmt.Sprintf("secrets: AWS SSO session for profile %q is expired or missing. "+
			"Run `aws sso login --profile %s` and rerun. "+
			"(The aws CLI may still work from its own cache; vctl needs a live SSO token.) "+
			"Skipping SM merge stage.", profile, profile)
	}
	return fmt.Sprintf("secrets: AWS credentials not available for profile %q — skipping SM merge stage: %v", profile, err)
}

// parseEnvKeys reads .env text and returns the set of keys (the text left of
// the first `=`). Tolerates blank lines and `#` comments. Quoted/unquoted
// values are both fine — we only care about the key.
func parseEnvKeys(content string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		out[strings.TrimSpace(line[:eq])] = struct{}{}
	}
	return out
}

func toSet(s []string) map[string]struct{} {
	out := make(map[string]struct{}, len(s))
	for _, v := range s {
		out[v] = struct{}{}
	}
	return out
}

func union(a, b map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		out[k] = struct{}{}
	}
	for k := range b {
		out[k] = struct{}{}
	}
	return out
}
