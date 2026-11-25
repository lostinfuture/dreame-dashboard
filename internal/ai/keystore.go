// Package ai provides optional, user-configured OpenAI air-quality analysis.
package ai

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"dreame-dashboard/internal/filex"
)

// DefaultModel is used when the user has not chosen a specific OpenAI model.
const DefaultModel = "gpt-4o-mini"

// ErrNotConfigured means no OpenAI API key is saved or supplied via env.
var ErrNotConfigured = errors.New("OpenAI API key is not configured")

// AllowedModels lists the selectable OpenAI models in the UI.
var AllowedModels = []string{
	"gpt-4o-mini",
	"gpt-4.1-mini",
	"gpt-4.1",
	"gpt-4o",
	"o4-mini",
}

// KeyStatus is the non-secret metadata safe to render in the UI.
type KeyStatus struct {
	Configured bool
	Saved      bool
	EnvLocked  bool
	Hint       string
	Model      string
	Source     string
	UpdatedAt  time.Time
}

type storedKey struct {
	Version    int       `json:"version"`
	Nonce      string    `json:"nonce"`
	Ciphertext string    `json:"ciphertext"`
	Hint       string    `json:"hint"`
	Model      string    `json:"model"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// KeyStore encrypts and persists the OpenAI API key at rest using AES-256-GCM.
type KeyStore struct {
	mu       sync.RWMutex
	dir      string
	envKey   string
	envModel string
}

// NewKeyStore creates a KeyStore backed by DataDir with optional env fallback.
func NewKeyStore(dir, envKey, envModel string) *KeyStore {
	return &KeyStore{
		dir:      dir,
		envKey:   strings.TrimSpace(envKey),
		envModel: NormalizeModel(envModel),
	}
}

// NormalizeModel validates a model name or returns DefaultModel.
func NormalizeModel(model string) string {
	m := strings.TrimSpace(model)
	for _, ok := range AllowedModels {
		if m == ok {
			return m
		}
	}
	return DefaultModel
}

// ValidModel reports whether model is in AllowedModels.
func ValidModel(model string) bool {
	m := strings.TrimSpace(model)
	for _, ok := range AllowedModels {
		if m == ok {
			return true
		}
	}
	return false
}

// MaskKey returns a display-safe hint that never exposes the full secret.
func MaskKey(key string) string {
	k := strings.TrimSpace(key)
	if len(k) <= 6 {
		return "••••"
	}
	prefix := k[:3]
	if strings.HasPrefix(k, "sk-proj-") && len(k) > 12 {
		prefix = "sk-proj-"
	} else if strings.HasPrefix(k, "sk-") {
		prefix = "sk-"
	}
	return prefix + "••••" + k[len(k)-4:]
}

// Status reports safe metadata without ever returning the plaintext key.
func (ks *KeyStore) Status() KeyStatus {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	model := DefaultModel
	if ks.envModel != "" {
		model = ks.envModel
	}
	rec, hasSaved := ks.readStoredLocked()
	if hasSaved && rec.Model != "" && ValidModel(rec.Model) {
		model = rec.Model
	}
	if ks.envKey != "" {
		return KeyStatus{
			Configured: true,
			Saved:      hasSaved,
			EnvLocked:  true,
			Hint:       MaskKey(ks.envKey),
			Model:      model,
			Source:     "env",
		}
	}
	if hasSaved && rec.Ciphertext != "" {
		return KeyStatus{
			Configured: true,
			Saved:      true,
			Hint:       rec.Hint,
			Model:      model,
			Source:     "saved",
			UpdatedAt:  rec.UpdatedAt,
		}
	}
	return KeyStatus{
		Configured: false,
		Model:      model,
		Source:     "none",
	}
}

// Resolve returns the active plaintext OpenAI API key and model.
func (ks *KeyStore) Resolve() (string, string, error) {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	model := DefaultModel
	if ks.envModel != "" {
		model = ks.envModel
	}
	rec, hasSaved := ks.readStoredLocked()
	if hasSaved && rec.Model != "" && ValidModel(rec.Model) {
		model = rec.Model
	}
	if ks.envKey != "" {
		return ks.envKey, model, nil
	}
	if !hasSaved || rec.Ciphertext == "" {
		return "", model, ErrNotConfigured
	}
	master, err := ks.loadMasterLocked(false)
	if err != nil {
		return "", model, fmt.Errorf("read encryption key: %w", err)
	}
	plain, err := decryptGCM(master, rec.Nonce, rec.Ciphertext)
	if err != nil {
		return "", model, fmt.Errorf("decrypt api key: %w", err)
	}
	if plain == "" {
		return "", model, ErrNotConfigured
	}
	return plain, model, nil
}

// Save encrypts and stores a new API key and/or updates the selected model.
// Passing an empty apiKey keeps the existing saved key while updating model.
func (ks *KeyStore) Save(apiKey, model string) error {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	if ks.dir == "" {
		return errors.New("no data directory configured")
	}
	key := strings.TrimSpace(apiKey)
	if model != "" && !ValidModel(model) {
		return errors.New("unsupported OpenAI model")
	}
	existing, hasExisting := ks.readStoredLocked()
	if key == "" {
		if !hasExisting && ks.envKey == "" {
			return errors.New("API key is required")
		}
		if model == "" {
			return nil
		}
		existing.Model = model
		existing.UpdatedAt = time.Now().UTC()
		if existing.Version == 0 {
			existing.Version = 1
		}
		return ks.writeStoredLocked(existing)
	}
	if len(key) < 16 || strings.ContainsAny(key, " \t\r\n") {
		return errors.New("API key format looks invalid")
	}
	master, err := ks.loadMasterLocked(true)
	if err != nil {
		return err
	}
	nonce, ct, err := encryptGCM(master, key)
	if err != nil {
		return err
	}
	chosenModel := existing.Model
	if model != "" {
		chosenModel = model
	}
	if chosenModel == "" || !ValidModel(chosenModel) {
		chosenModel = DefaultModel
	}
	rec := storedKey{
		Version:    1,
		Nonce:      nonce,
		Ciphertext: ct,
		Hint:       MaskKey(key),
		Model:      chosenModel,
		UpdatedAt:  time.Now().UTC(),
	}
	return ks.writeStoredLocked(rec)
}

// Delete removes the saved encrypted OpenAI key file.
func (ks *KeyStore) Delete() error {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	if ks.dir == "" {
		return nil
	}
	err := os.Remove(filepath.Join(ks.dir, "openai_key.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (ks *KeyStore) keyFilePath() string {
	return filepath.Join(ks.dir, "openai_key.json")
}

func (ks *KeyStore) masterFilePath() string {
	return filepath.Join(ks.dir, ".secret.key")
}

func (ks *KeyStore) readStoredLocked() (storedKey, bool) {
	if ks.dir == "" {
		return storedKey{}, false
	}
	b, err := os.ReadFile(ks.keyFilePath())
	if err != nil {
		return storedKey{}, false
	}
	var rec storedKey
	if json.Unmarshal(b, &rec) != nil || rec.Version != 1 {
		return storedKey{}, false
	}
	return rec, true
}

func (ks *KeyStore) writeStoredLocked(rec storedKey) error {
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return filex.WriteAtomic(ks.keyFilePath(), b)
}

func (ks *KeyStore) loadMasterLocked(create bool) ([]byte, error) {
	path := ks.masterFilePath()
	b, err := os.ReadFile(path)
	if err == nil {
		raw, derr := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(b)))
		if derr == nil && len(raw) == 32 {
			return raw, nil
		}
		if !create {
			return nil, errors.New("corrupt secret key")
		}
	} else if !errors.Is(err, os.ErrNotExist) || !create {
		return nil, err
	}
	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return nil, err
	}
	enc := []byte(base64.RawStdEncoding.EncodeToString(raw) + "\n")
	if err := filex.WriteAtomic(path, enc); err != nil {
		return nil, err
	}
	return raw, nil
}

func encryptGCM(master []byte, plain string) (nonceB64, ctB64 string, err error) {
	block, err := aes.NewCipher(master)
	if err != nil {
		return "", "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", "", err
	}
	ct := gcm.Seal(nil, nonce, []byte(plain), nil)
	return base64.RawStdEncoding.EncodeToString(nonce), base64.RawStdEncoding.EncodeToString(ct), nil
}

func decryptGCM(master []byte, nonceB64, ctB64 string) (string, error) {
	nonce, err := base64.RawStdEncoding.DecodeString(nonceB64)
	if err != nil {
		return "", err
	}
	ct, err := base64.RawStdEncoding.DecodeString(ctB64)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(master)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
