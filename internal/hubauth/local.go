package hubauth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // RFC 6238's required default algorithm.
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

const (
	DefaultLocalAuthPath                = "/etc/bitacora/local-auth.json"
	DefaultLocalAuthKeyPath             = "/etc/bitacora/local-auth.key"
	localAuthFileMode       os.FileMode = 0o600
	localAuthSaltBytes                  = 16
	localAuthKeyBytes                   = 32
	localAuthRecoveryCount              = 10
	localAuthRecoveryBytes              = 16
	localAuthLockout                    = 15 * time.Minute
	localAuthMaxFailures                = 5
	totpPeriod                          = 30 * time.Second
)

var (
	ErrLocalAuthNotConfigured  = errors.New("local authentication is not configured")
	ErrLocalAuthDisabled       = errors.New("local authentication is disabled")
	ErrLocalAuthLocked         = errors.New("local authentication is temporarily locked")
	ErrInvalidLocalCredentials = errors.New("invalid local credentials")
	codePattern                = regexp.MustCompile(`^[0-9]{6}$`)
)

// LocalStore owns the only persistent local credential. Its mutex makes every
// mutation (including consuming a recovery code) a single read-modify-write
// transaction; writeState replaces the file atomically.
type LocalStore struct {
	path    string
	keyPath string
	mu      sync.Mutex
	now     func() time.Time
}

type localAuthState struct {
	Version           int             `json:"version"`
	Password          passwordHash    `json:"password"`
	TOTP              encryptedSecret `json:"totp"`
	Recovery          []passwordHash  `json:"recovery_codes"`
	Failures          int             `json:"failures"`
	LockedUntil       time.Time       `json:"locked_until,omitempty"`
	SessionGeneration uint64          `json:"session_generation"`
	Disabled          bool            `json:"disabled,omitempty"`
}

type passwordHash struct {
	Salt string `json:"salt"`
	Hash string `json:"hash"`
}

type encryptedSecret struct {
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

// NewLocalStore supplies a store rooted at explicit paths. It is intended for
// production wiring and tests; no passwords or secrets are accepted here.
func NewLocalStore(path, keyPath string) *LocalStore {
	return &LocalStore{path: path, keyPath: keyPath, now: time.Now}
}

// OpenLocalStore opens an initialized store. Absence of both files is the
// supported disabled-by-default state; a partial credential is a boot error.
func OpenLocalStore(path, keyPath string) (*LocalStore, error) {
	_, stateErr := os.Stat(path)
	_, keyErr := os.Stat(keyPath)
	if errors.Is(stateErr, os.ErrNotExist) && errors.Is(keyErr, os.ErrNotExist) {
		return nil, nil
	}
	if stateErr != nil {
		return nil, fmt.Errorf("checking local authentication state: %w", stateErr)
	}
	if keyErr != nil {
		return nil, fmt.Errorf("checking local authentication key: %w", keyErr)
	}
	store := NewLocalStore(path, keyPath)
	if _, err := store.readState(); err != nil {
		return nil, err
	}
	key, err := store.readKey()
	if err != nil {
		return nil, err
	}
	zero(key)
	return store, nil
}

func (s *LocalStore) IsEnabled() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.readState()
	return err == nil && !state.Disabled
}

// Initialize creates the encryption key and the credential. The returned
// secret and recovery codes are the only plaintext copies this package emits.
func (s *LocalStore) Initialize(password string) (string, []string, error) {
	if s == nil {
		return "", nil, errors.New("local authentication store is nil")
	}
	if password == "" {
		return "", nil, errors.New("password must not be empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, path := range []string{s.path, s.keyPath} {
		if _, err := os.Stat(path); err == nil {
			return "", nil, errors.New("local authentication is already initialized or incomplete")
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", nil, fmt.Errorf("checking local authentication files: %w", err)
		}
	}
	key, err := randomBytes(localAuthKeyBytes)
	if err != nil {
		return "", nil, err
	}
	defer zero(key)
	if err := writePrivateFile(s.keyPath, key); err != nil {
		return "", nil, fmt.Errorf("writing local authentication key: %w", err)
	}
	secret, recovery, err := s.newSecondFactor(key)
	if err != nil {
		return "", nil, err
	}
	passwordRecord, err := hashSecret([]byte(password))
	if err != nil {
		return "", nil, err
	}
	state := localAuthState{Version: 1, Password: passwordRecord, TOTP: secret.encryptedSecret, Recovery: recovery.hashes, SessionGeneration: 1}
	if err := s.writeState(state); err != nil {
		return "", nil, err
	}
	return secret.plaintext, recovery.plaintext, nil
}

// RotatePassword replaces the password and invalidates all local sessions.
func (s *LocalStore) RotatePassword(password string) error {
	if password == "" {
		return errors.New("password must not be empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.readState()
	if err != nil {
		return err
	}
	record, err := hashSecret([]byte(password))
	if err != nil {
		return err
	}
	state.Password = record
	state.Failures, state.LockedUntil = 0, time.Time{}
	state.SessionGeneration++
	return s.writeState(state)
}

// RotateTOTP creates a replacement TOTP secret and recovery set.
func (s *LocalStore) RotateTOTP() (string, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.readState()
	if err != nil {
		return "", nil, err
	}
	key, err := s.readKey()
	if err != nil {
		return "", nil, err
	}
	defer zero(key)
	secret, recovery, err := s.newSecondFactor(key)
	if err != nil {
		return "", nil, err
	}
	state.TOTP, state.Recovery = secret.encryptedSecret, recovery.hashes
	state.Failures, state.LockedUntil = 0, time.Time{}
	state.SessionGeneration++
	if err := s.writeState(state); err != nil {
		return "", nil, err
	}
	return secret.plaintext, recovery.plaintext, nil
}

// RegenerateRecoveryCodes replaces every prior code and invalidates sessions.
func (s *LocalStore) RegenerateRecoveryCodes() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.readState()
	if err != nil {
		return nil, err
	}
	recovery, err := newRecoveryCodes()
	if err != nil {
		return nil, err
	}
	state.Recovery = recovery.hashes
	state.SessionGeneration++
	if err := s.writeState(state); err != nil {
		return nil, err
	}
	return recovery.plaintext, nil
}

// Disable leaves a generation marker on disk so all live local sessions are
// rejected immediately and the source cannot be silently re-enabled.
func (s *LocalStore) Disable() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.readState()
	if err != nil {
		return err
	}
	state.Disabled = true
	state.SessionGeneration++
	return s.writeState(state)
}

// VerifyPassword verifies the current local password for a TTY-only
// credential-management command. It deliberately does not create a session or
// consume a second factor: local administrative access is the recovery path
// ADR-0023 reserves for rotating an otherwise inaccessible credential.
func (s *LocalStore) VerifyPassword(password string) error {
	if s == nil {
		return ErrLocalAuthNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.readState()
	if err != nil {
		return err
	}
	if state.Disabled {
		return ErrLocalAuthDisabled
	}
	ok, err := verifySecret(state.Password, []byte(password))
	if err != nil {
		return err
	}
	if !ok {
		return ErrInvalidLocalCredentials
	}
	return nil
}

// Authenticate verifies password plus TOTP or one recovery code. Failed
// attempts and lockout are persisted before the error returns.
func (s *LocalStore) Authenticate(password, factor string) (uint64, error) {
	if s == nil {
		return 0, ErrLocalAuthNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.readState()
	if err != nil {
		return 0, err
	}
	if state.Disabled {
		return 0, ErrLocalAuthDisabled
	}
	now := s.now().UTC()
	if now.Before(state.LockedUntil) {
		return 0, ErrLocalAuthLocked
	}
	validPassword, err := verifySecret(state.Password, []byte(password))
	if err != nil {
		return 0, err
	}
	validFactor := false
	usedRecovery := -1
	if validPassword {
		key, err := s.readKey()
		if err != nil {
			return 0, err
		}
		defer zero(key)
		secret, err := decryptSecret(key, state.TOTP)
		if err != nil {
			return 0, err
		}
		validFactor = validTOTP(secret, factor, now)
		zero(secret)
		if !validFactor {
			for i, record := range state.Recovery {
				ok, err := verifySecret(record, []byte(factor))
				if err != nil {
					return 0, err
				}
				if ok {
					validFactor, usedRecovery = true, i
					break
				}
			}
		}
	}
	if !validPassword || !validFactor {
		state.Failures++
		if state.Failures >= localAuthMaxFailures {
			state.LockedUntil = now.Add(localAuthLockout)
		}
		if err := s.writeState(state); err != nil {
			return 0, err
		}
		return 0, ErrInvalidLocalCredentials
	}
	if usedRecovery >= 0 {
		state.Recovery = append(state.Recovery[:usedRecovery], state.Recovery[usedRecovery+1:]...)
	}
	state.Failures, state.LockedUntil = 0, time.Time{}
	if err := s.writeState(state); err != nil {
		return 0, err
	}
	return state.SessionGeneration, nil
}

func (s *LocalStore) generationValid(generation uint64) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.readState()
	return err == nil && !state.Disabled && state.SessionGeneration == generation
}

type generatedSecondFactor struct {
	encryptedSecret
	plaintext string
}
type generatedRecovery struct {
	hashes    []passwordHash
	plaintext []string
}

func (s *LocalStore) newSecondFactor(key []byte) (generatedSecondFactor, generatedRecovery, error) {
	secret, err := randomBytes(20)
	if err != nil {
		return generatedSecondFactor{}, generatedRecovery{}, err
	}
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
	encrypted, err := encryptSecret(key, secret)
	zero(secret)
	if err != nil {
		return generatedSecondFactor{}, generatedRecovery{}, err
	}
	recovery, err := newRecoveryCodes()
	if err != nil {
		return generatedSecondFactor{}, generatedRecovery{}, err
	}
	return generatedSecondFactor{encryptedSecret: encrypted, plaintext: encoded}, recovery, nil
}

func newRecoveryCodes() (generatedRecovery, error) {
	result := generatedRecovery{hashes: make([]passwordHash, 0, localAuthRecoveryCount), plaintext: make([]string, 0, localAuthRecoveryCount)}
	for range localAuthRecoveryCount {
		value, err := randomBytes(localAuthRecoveryBytes)
		if err != nil {
			return generatedRecovery{}, err
		}
		encoded := base64.RawURLEncoding.EncodeToString(value)
		zero(value)
		hash, err := hashSecret([]byte(encoded))
		if err != nil {
			return generatedRecovery{}, err
		}
		result.hashes, result.plaintext = append(result.hashes, hash), append(result.plaintext, encoded)
	}
	return result, nil
}

func hashSecret(secret []byte) (passwordHash, error) {
	salt, err := randomBytes(localAuthSaltBytes)
	if err != nil {
		return passwordHash{}, err
	}
	hash := argon2.IDKey(secret, salt, 3, 64*1024, 4, localAuthKeyBytes)
	result := passwordHash{Salt: base64.RawStdEncoding.EncodeToString(salt), Hash: base64.RawStdEncoding.EncodeToString(hash)}
	zero(salt)
	zero(hash)
	return result, nil
}

func verifySecret(record passwordHash, secret []byte) (bool, error) {
	salt, err := base64.RawStdEncoding.DecodeString(record.Salt)
	if err != nil || len(salt) != localAuthSaltBytes {
		return false, errors.New("invalid local authentication salt")
	}
	expected, err := base64.RawStdEncoding.DecodeString(record.Hash)
	if err != nil || len(expected) != localAuthKeyBytes {
		return false, errors.New("invalid local authentication hash")
	}
	actual := argon2.IDKey(secret, salt, 3, 64*1024, 4, localAuthKeyBytes)
	ok := subtle.ConstantTimeCompare(actual, expected) == 1
	zero(salt)
	zero(expected)
	zero(actual)
	return ok, nil
}

func encryptSecret(key, secret []byte) (encryptedSecret, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return encryptedSecret{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return encryptedSecret{}, err
	}
	nonce, err := randomBytes(gcm.NonceSize())
	if err != nil {
		return encryptedSecret{}, err
	}
	ciphertext := gcm.Seal(nil, nonce, secret, nil)
	result := encryptedSecret{Nonce: base64.RawStdEncoding.EncodeToString(nonce), Ciphertext: base64.RawStdEncoding.EncodeToString(ciphertext)}
	zero(nonce)
	zero(ciphertext)
	return result, nil
}

func decryptSecret(key []byte, encrypted encryptedSecret) ([]byte, error) {
	nonce, err := base64.RawStdEncoding.DecodeString(encrypted.Nonce)
	if err != nil {
		return nil, errors.New("invalid local authentication nonce")
	}
	ciphertext, err := base64.RawStdEncoding.DecodeString(encrypted.Ciphertext)
	if err != nil {
		return nil, errors.New("invalid local authentication ciphertext")
	}
	defer zero(nonce)
	defer zero(ciphertext)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, errors.New("invalid local authentication nonce")
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, errors.New("decrypting local authentication secret")
	}
	return plain, nil
}

func validTOTP(secret []byte, value string, now time.Time) bool {
	if !codePattern.MatchString(value) {
		return false
	}
	for _, offset := range []int64{-1, 0, 1} {
		counter := uint64(now.Unix()/int64(totpPeriod.Seconds()) + offset)
		var message [8]byte
		binary.BigEndian.PutUint64(message[:], counter)
		mac := hmac.New(sha1.New, secret)
		_, _ = mac.Write(message[:])
		sum := mac.Sum(nil)
		offset := sum[len(sum)-1] & 0x0f
		code := (uint32(sum[offset]&0x7f)<<24 | uint32(sum[offset+1])<<16 | uint32(sum[offset+2])<<8 | uint32(sum[offset+3])) % 1_000_000
		candidate := fmt.Sprintf("%06d", code)
		zero(sum)
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(value)) == 1 {
			return true
		}
	}
	return false
}

func (s *LocalStore) readState() (localAuthState, error) {
	if err := requirePrivateFile(s.path); err != nil {
		return localAuthState{}, err
	}
	bytes, err := os.ReadFile(s.path)
	if err != nil {
		return localAuthState{}, fmt.Errorf("reading local authentication state: %w", err)
	}
	var state localAuthState
	if err := json.Unmarshal(bytes, &state); err != nil {
		return localAuthState{}, fmt.Errorf("decoding local authentication state: %w", err)
	}
	if state.Version != 1 || len(state.Recovery) > localAuthRecoveryCount || state.Password.Salt == "" || state.TOTP.Nonce == "" {
		return localAuthState{}, errors.New("invalid local authentication state")
	}
	return state, nil
}

func (s *LocalStore) writeState(state localAuthState) error {
	bytes, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return writePrivateFile(s.path, append(bytes, '\n'))
}

func (s *LocalStore) readKey() ([]byte, error) {
	if err := requirePrivateFile(s.keyPath); err != nil {
		return nil, err
	}
	key, err := os.ReadFile(s.keyPath)
	if err != nil {
		return nil, fmt.Errorf("reading local authentication key: %w", err)
	}
	if len(key) != localAuthKeyBytes {
		zero(key)
		return nil, errors.New("invalid local authentication key")
	}
	return key, nil
}

func writePrivateFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".local-auth-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(localAuthFileMode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return os.Chmod(path, localAuthFileMode)
}

func requirePrivateFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("checking local authentication file: %w", err)
	}
	if info.Mode().Perm() != localAuthFileMode {
		return fmt.Errorf("local authentication file %s must have mode %04o", path, localAuthFileMode)
	}
	return nil
}

func randomBytes(size int) ([]byte, error) {
	bytes := make([]byte, size)
	_, err := io.ReadFull(rand.Reader, bytes)
	return bytes, err
}
func zero(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
