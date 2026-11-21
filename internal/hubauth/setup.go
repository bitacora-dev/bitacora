package hubauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ADR-0025 setup tokens: the proof of local administrative access that lets
// the web interface create or replace the single local:operator credential.
const (
	// DefaultSetupTokenTTL bounds how long a printed setup token stays valid.
	DefaultSetupTokenTTL = 30 * time.Minute
	// MaxSetupTokenTTL caps how long a printed token may live: the console
	// or journal that carries it is sensitive for that whole window.
	MaxSetupTokenTTL = 24 * time.Hour
	// MinLocalPasswordLength is the shortest password the web setup accepts.
	MinLocalPasswordLength = 12
	maxLocalPasswordBytes  = 1024
	maxLocalUsernameRunes  = 64
	setupTokenBytes        = 32
	setupTokenFileName     = "local-auth-setup.json"
)

// SetupKind says why a setup token exists. Both kinds are redeemed by the same
// web flow; they differ only in what they are allowed to replace.
type SetupKind string

const (
	// SetupInstall is issued by the hub itself on a start without an
	// initialized credential. It can never overwrite an existing one.
	SetupInstall SetupKind = "install"
	// SetupRecovery is issued by `bitacora-hub auth local recover` after a
	// total loss. Redeeming it replaces the whole credential.
	SetupRecovery SetupKind = "recovery"
)

var (
	// ErrSetupStateNotWritable means the credential directory is read-only.
	// It is checked before the token is consumed, so the operator can fix the
	// mount and retry with the same token.
	ErrSetupStateNotWritable = errors.New("the local credential directory is not writable")
	// ErrSetupTokenInvalid deliberately covers unknown, expired, consumed and
	// superseded tokens alike: the caller learns nothing about which.
	ErrSetupTokenInvalid = errors.New("setup token is invalid, expired or already used")
	// ErrSetupInvalidUsername and the password errors are input validation,
	// returned before anything is persisted.
	ErrSetupInvalidUsername         = errors.New("username must be 1 to 64 printable characters")
	ErrSetupPasswordTooShort        = fmt.Errorf("password must be at least %d characters", MinLocalPasswordLength)
	ErrSetupPasswordTooLong         = fmt.Errorf("password must be at most %d bytes", maxLocalPasswordBytes)
	ErrSetupPasswordMatchesUsername = errors.New("password must not equal the username")
)

// setupTokenState is everything persisted about a setup token: its hash, its
// expiry and whether it was consumed. The plaintext never touches the disk.
type setupTokenState struct {
	Version    int       `json:"version"`
	Kind       SetupKind `json:"kind"`
	Hash       string    `json:"hash"`
	ExpiresAt  time.Time `json:"expires_at"`
	Consumed   bool      `json:"consumed"`
	ConsumedAt time.Time `json:"consumed_at,omitzero"`
}

// SetupTokenPath is where the setup token state lives: next to the credential,
// so the persistent volume that ADR-0025 requires for the credential covers it
// too.
func (s *LocalStore) SetupTokenPath() string {
	return filepath.Join(filepath.Dir(s.path), setupTokenFileName)
}

// Initialized reports whether a credential file exists, enabled or not. It is
// what separates a fresh installation (which gets an install token) from a
// disabled credential (which needs the explicit local recovery command).
func (s *LocalStore) Initialized() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.readState()
	return !errors.Is(err, ErrLocalAuthNotInitialized)
}

// IssueSetupToken creates a new single-use token and invalidates any previous
// one, whatever its kind. It returns the only plaintext copy; the caller must
// deliver it through a channel that already requires local administrative
// access (the hub's console/journal, or the CLI's stdout).
func (s *LocalStore) IssueSetupToken(kind SetupKind, ttl time.Duration) (string, time.Time, error) {
	if s == nil {
		return "", time.Time{}, ErrLocalAuthNotConfigured
	}
	if kind != SetupInstall && kind != SetupRecovery {
		return "", time.Time{}, fmt.Errorf("unknown setup token kind %q", kind)
	}
	if err := ValidateSetupTokenTTL(ttl); err != nil {
		return "", time.Time{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if kind == SetupInstall {
		if _, err := s.readState(); !errors.Is(err, ErrLocalAuthNotInitialized) {
			return "", time.Time{}, errors.New("an install token can only be issued before the local credential exists; use `bitacora-hub auth local recover`")
		}
	}
	raw, err := randomBytes(setupTokenBytes)
	if err != nil {
		return "", time.Time{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	zero(raw)
	expiresAt := s.now().UTC().Add(ttl)
	state := setupTokenState{Version: 1, Kind: kind, Hash: hashSetupToken(token), ExpiresAt: expiresAt}
	if err := s.writeSetupState(state); err != nil {
		return "", time.Time{}, fmt.Errorf("writing setup token state: %w", err)
	}
	return token, expiresAt, nil
}

// ValidateSetupTokenTTL rejects a lifetime that is not positive or exceeds
// MaxSetupTokenTTL.
func ValidateSetupTokenTTL(ttl time.Duration) error {
	if ttl <= 0 || ttl > MaxSetupTokenTTL {
		return fmt.Errorf("setup token lifetime must be greater than 0 and at most %s, got %s", MaxSetupTokenTTL, ttl)
	}
	return nil
}

// CheckSetupWritable verifies that the credential and key directories accept
// new files. A read-only mount would otherwise surface only after the token
// had been consumed.
func (s *LocalStore) CheckSetupWritable() error {
	if s == nil {
		return ErrLocalAuthNotConfigured
	}
	seen := map[string]bool{}
	for _, dir := range []string{filepath.Dir(s.path), filepath.Dir(s.keyPath)} {
		if seen[dir] {
			continue
		}
		seen[dir] = true
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("%w: %s: %v", ErrSetupStateNotWritable, dir, err)
		}
		probe, err := os.CreateTemp(dir, ".local-auth-probe-")
		if err != nil {
			return fmt.Errorf("%w: %s: %v", ErrSetupStateNotWritable, dir, err)
		}
		name := probe.Name()
		closeErr := probe.Close()
		removeErr := os.Remove(name)
		if closeErr != nil || removeErr != nil {
			return fmt.Errorf("%w: %s", ErrSetupStateNotWritable, dir)
		}
	}
	return nil
}

// SetupTokenPending reports whether a usable setup token exists. It reveals
// nothing about the token, only that the setup screen has something to redeem.
// It takes no lock, so it never waits behind a login's Argon2id.
func (s *LocalStore) SetupTokenPending() bool {
	if s == nil {
		return false
	}
	state, err := s.readSetupState()
	return err == nil && s.usable(state)
}

// CheckSetupToken validates a token without consuming it. The web flow calls
// it before showing the TOTP enrolment, so a mistyped token fails early.
func (s *LocalStore) CheckSetupToken(token string) (SetupKind, error) {
	if s == nil {
		return "", ErrLocalAuthNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.lookupSetupTokenLocked(token)
	if err != nil {
		return "", err
	}
	return state.Kind, nil
}

// CompleteSetup redeems a setup token: it consumes the token and then writes a
// complete credential (username, password hash, encrypted TOTP secret and ten
// fresh recovery codes). The returned recovery codes are the only plaintext
// copies.
//
// The token is marked consumed before the credential is written, on purpose:
// a failure in between leaves an unusable token (issue another one) rather
// than a token that could be replayed to overwrite the credential.
//
// Replacing an existing credential increments its session generation, which
// revokes every live local session.
func (s *LocalStore) CompleteSetup(token, username string, password passwordHash, totpSecret []byte) (kind SetupKind, recoveryCodes []string, generation uint64, err error) {
	if s == nil {
		return "", nil, 0, ErrLocalAuthNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	setup, err := s.lookupSetupTokenLocked(token)
	if err != nil {
		return "", nil, 0, err
	}
	if err := s.CheckSetupWritable(); err != nil {
		return "", nil, 0, err
	}
	setup.Consumed, setup.ConsumedAt = true, s.now().UTC()
	if err := s.writeSetupState(setup); err != nil {
		return "", nil, 0, fmt.Errorf("consuming setup token: %w", err)
	}

	previous, stateErr := s.readState()
	fresh := errors.Is(stateErr, ErrLocalAuthNotInitialized)
	switch {
	case fresh:
		generation = 1
	case stateErr == nil:
		generation = previous.SessionGeneration + 1
	default:
		// An unreadable credential is exactly what total recovery exists
		// for. Its old generation is unknown, so pick one no earlier
		// session can carry.
		generation = uint64(s.now().UnixNano())
	}

	key, createdKey, err := s.keyForSetupLocked()
	if err != nil {
		return "", nil, 0, err
	}
	defer zero(key)
	defer func() {
		if err != nil && createdKey && fresh {
			_ = os.Remove(s.keyPath)
		}
	}()
	encrypted, err := encryptSecret(key, totpSecret)
	if err != nil {
		return "", nil, 0, err
	}
	recovery, err := newRecoveryCodes()
	if err != nil {
		return "", nil, 0, err
	}
	state := localAuthState{
		Version:           1,
		Username:          username,
		Password:          password,
		TOTP:              encrypted,
		Recovery:          recovery.hashes,
		SessionGeneration: generation,
	}
	if err := s.writeState(state); err != nil {
		return "", nil, 0, fmt.Errorf("writing local authentication state: %w", err)
	}
	return setup.Kind, recovery.plaintext, generation, nil
}

// keyForSetupLocked reuses a valid key and replaces a missing or unreadable
// one. The TOTP secret is re-encrypted on every setup, so a new key loses
// nothing.
func (s *LocalStore) keyForSetupLocked() ([]byte, bool, error) {
	if key, err := s.readKey(); err == nil {
		return key, false, nil
	}
	key, err := randomBytes(localAuthKeyBytes)
	if err != nil {
		return nil, false, err
	}
	if err := writePrivateFile(s.keyPath, key); err != nil {
		zero(key)
		return nil, false, fmt.Errorf("writing local authentication key: %w", err)
	}
	return key, true, nil
}

func (s *LocalStore) lookupSetupTokenLocked(token string) (setupTokenState, error) {
	state, err := s.readSetupState()
	if err != nil {
		return setupTokenState{}, ErrSetupTokenInvalid
	}
	if !s.usable(state) {
		return setupTokenState{}, ErrSetupTokenInvalid
	}
	if subtle.ConstantTimeCompare([]byte(hashSetupToken(token)), []byte(state.Hash)) != 1 {
		return setupTokenState{}, ErrSetupTokenInvalid
	}
	return state, nil
}

// usable takes no lock: both files are replaced by atomic rename, so reading
// them is safe while a login holds s.mu. It applies every rule a token must pass apart from matching:
// unconsumed, unexpired and, for an install token, no credential yet.
func (s *LocalStore) usable(state setupTokenState) bool {
	if state.Consumed || !s.now().UTC().Before(state.ExpiresAt) {
		return false
	}
	if state.Kind == SetupInstall {
		_, err := s.readState()
		return errors.Is(err, ErrLocalAuthNotInitialized)
	}
	return state.Kind == SetupRecovery
}

func (s *LocalStore) readSetupState() (setupTokenState, error) {
	path := s.SetupTokenPath()
	if err := requirePrivateFile(path); err != nil {
		return setupTokenState{}, err
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return setupTokenState{}, err
	}
	var state setupTokenState
	if err := json.Unmarshal(bytes, &state); err != nil {
		return setupTokenState{}, err
	}
	if state.Version != 1 || state.Hash == "" {
		return setupTokenState{}, errors.New("invalid setup token state")
	}
	return state, nil
}

func (s *LocalStore) writeSetupState(state setupTokenState) error {
	bytes, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return writePrivateFile(s.SetupTokenPath(), append(bytes, '\n'))
}

// hashSetupToken is a plain SHA-256: the token carries 256 bits of entropy, so
// unlike a password it needs no slow hash to resist guessing.
func hashSetupToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ValidateSetupInput applies the server-side rules for the web setup form.
// The username is a free login attribute (ADR-0025): it is not an email and it
// never becomes a second subject.
func ValidateSetupInput(username, password string) (string, error) {
	username = strings.TrimSpace(username)
	if username == "" || utf8.RuneCountInString(username) > maxLocalUsernameRunes {
		return "", ErrSetupInvalidUsername
	}
	for _, r := range username {
		if !unicode.IsPrint(r) {
			return "", ErrSetupInvalidUsername
		}
	}
	if len(password) > maxLocalPasswordBytes {
		return "", ErrSetupPasswordTooLong
	}
	if utf8.RuneCountInString(password) < MinLocalPasswordLength {
		return "", ErrSetupPasswordTooShort
	}
	if strings.EqualFold(password, username) {
		return "", ErrSetupPasswordMatchesUsername
	}
	return username, nil
}

// newTOTPSecret returns a fresh RFC 6238 secret and its base32 rendering.
func newTOTPSecret() ([]byte, string, error) {
	secret, err := randomBytes(20)
	if err != nil {
		return nil, "", err
	}
	return secret, totpEncoding.EncodeToString(secret), nil
}
