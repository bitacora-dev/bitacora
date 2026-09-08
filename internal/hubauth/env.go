package hubauth

import (
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
