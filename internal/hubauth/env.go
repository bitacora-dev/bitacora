package hubauth

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Environment variables that configure ADR-0019 authentication. They are
// environment and not flags on purpose: a client secret passed as a flag is
// visible to anyone who can run ps on the host.
const (
	EnvIssuer          = "BITACORA_OIDC_ISSUER"
	EnvClientID        = "BITACORA_OIDC_CLIENT_ID"
	EnvClientSecret    = "BITACORA_OIDC_CLIENT_SECRET"
	EnvRedirectURL     = "BITACORA_OIDC_REDIRECT_URL"
	EnvSessionTTL      = "BITACORA_OIDC_SESSION_TTL"
	EnvInsecureCookies = "BITACORA_OIDC_INSECURE_COOKIES"
)

// ConfigFromEnv reads the operator configuration. Leaving the variables unset
// is the supported way to keep the hub behaving as it did before ADR-0019.
func ConfigFromEnv() Config {
	cfg := Config{
		Issuer:       os.Getenv(EnvIssuer),
		ClientID:     os.Getenv(EnvClientID),
		ClientSecret: os.Getenv(EnvClientSecret),
		RedirectURL:  os.Getenv(EnvRedirectURL),
	}
	if raw := os.Getenv(EnvSessionTTL); raw != "" {
		if ttl, err := time.ParseDuration(raw); err == nil && ttl > 0 {
			cfg.SessionTTL = ttl
		}
	}
	if raw := os.Getenv(EnvInsecureCookies); raw != "" {
		if insecure, err := strconv.ParseBool(raw); err == nil {
			cfg.InsecureCookies = insecure
		}
	}
	return cfg
}

// Environment variables that configure the ADR-0023 local credential.
//
// The paths are configurable because ADR-0025 requires a container deployment
// to keep credential and key state on a persistent volume, which is not
// necessarily the /etc/bitacora of a systemd installation. No secret travels
// through any of them: they name files, never passwords or TOTP material.
const (
	EnvLocalAuth        = "BITACORA_LOCAL_AUTH"
	EnvLocalAuthPath    = "BITACORA_LOCAL_AUTH_PATH"
	EnvLocalAuthKeyPath = "BITACORA_LOCAL_AUTH_KEY_PATH"
)

// LocalConfig is the operator's local-authentication configuration.
type LocalConfig struct {
	// Required turns the local source on. It is what makes activation
	// explicit instead of a side effect of a file existing, and it is
	// deliberately one-way: it can bring the boundary up before a credential
	// exists, but setting it to false does not take down an initialized
	// credential. Honouring false as "disable" would mean a deploy-time typo
	// silently removes the only human boundary in front of the dashboard.
	// Turning the source off is `bitacora-hub auth local disable`, which
	// persists the decision and revokes live sessions.
	Required bool
	// Path and KeyPath override the ADR-0023 defaults when non-empty.
	Path    string
	KeyPath string
}

// LocalConfigFromEnv reads the local-authentication configuration. An
// unparseable switch is an error rather than a silent false: a deployment that
// looks authenticated and is not is the failure this switch exists to prevent.
func LocalConfigFromEnv() (LocalConfig, error) {
	cfg := LocalConfig{
		Path:    os.Getenv(EnvLocalAuthPath),
		KeyPath: os.Getenv(EnvLocalAuthKeyPath),
	}
	if raw := os.Getenv(EnvLocalAuth); raw != "" {
		required, err := strconv.ParseBool(raw)
		if err != nil {
			return LocalConfig{}, fmt.Errorf("%s must be a boolean, got %q", EnvLocalAuth, raw)
		}
		cfg.Required = required
	}
	return cfg, nil
}

// StatePath is where the credential lives.
func (c LocalConfig) StatePath() string {
	if c.Path != "" {
		return c.Path
	}
	return DefaultLocalAuthPath
}

// KeyStatePath is where the key that protects the TOTP secret lives.
func (c LocalConfig) KeyStatePath() string {
	if c.KeyPath != "" {
		return c.KeyPath
	}
	return DefaultLocalAuthKeyPath
}
