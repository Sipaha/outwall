// Package secret implements the master-password vault: Argon2id key derivation
// and per-secret AES-256-GCM encryption. The derived key lives only in memory
// while unlocked; the master password itself is never stored.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"golang.org/x/crypto/argon2"

	"github.com/Sipaha/outwall/internal/store"
)

var (
	ErrNotInitialized = errors.New("vault not initialized")
	ErrBadPassword    = errors.New("incorrect master password")
	ErrLocked         = errors.New("vault is locked")
)

// Argon2id parameters (see Global Constraints).
const (
	argonTime    = 1
	argonMemory  = 64 * 1024
	argonThreads = 4
	keyLen       = 32
	saltLen      = 16
	nonceLen     = 12
)

// verifierPlaintext is encrypted at Init and decrypted at Unlock to check the password.
var verifierPlaintext = []byte("outwall-vault-verifier-v1")

// Vault holds the derived key in memory while unlocked.
type Vault struct {
	store *store.Store
	mu    sync.RWMutex
	key   []byte // nil when locked
}

// NewVault constructs a vault backed by the given store.
func NewVault(s *store.Store) *Vault { return &Vault{store: s} }

// Initialized reports whether a vault_meta row exists.
func (v *Vault) Initialized() (bool, error) {
	var n int
	err := v.store.DB().QueryRow(`SELECT COUNT(*) FROM vault_meta WHERE id=1`).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("query vault_meta: %w", err)
	}
	return n > 0, nil
}

func deriveKey(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, keyLen)
}

// Init performs first-time setup and leaves the vault unlocked.
func (v *Vault) Init(password string) error {
	init, err := v.Initialized()
	if err != nil {
		return err
	}
	if init {
		return errors.New("vault already initialized")
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("read salt: %w", err)
	}
	key := deriveKey(password, salt)
	verifier, err := sealWith(key, verifierPlaintext)
	if err != nil {
		return err
	}
	if _, err := v.store.DB().Exec(
		`INSERT INTO vault_meta (id, salt, verifier) VALUES (1, ?, ?)`, salt, verifier,
	); err != nil {
		return fmt.Errorf("store vault_meta: %w", err)
	}
	v.mu.Lock()
	v.key = key
	v.mu.Unlock()
	return nil
}

// Unlock derives the key and verifies it against the stored verifier.
func (v *Vault) Unlock(password string) error {
	var salt, verifier []byte
	err := v.store.DB().QueryRow(`SELECT salt, verifier FROM vault_meta WHERE id=1`).Scan(&salt, &verifier)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotInitialized
	}
	if err != nil {
		return fmt.Errorf("load vault_meta: %w", err)
	}
	key := deriveKey(password, salt)
	got, err := openWith(key, verifier)
	if err != nil || string(got) != string(verifierPlaintext) {
		return ErrBadPassword
	}
	v.mu.Lock()
	v.key = key
	v.mu.Unlock()
	return nil
}

// Lock zeroes the in-memory key.
func (v *Vault) Lock() {
	v.mu.Lock()
	for i := range v.key {
		v.key[i] = 0
	}
	v.key = nil
	v.mu.Unlock()
}

// Locked reports whether the vault is currently locked.
func (v *Vault) Locked() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.key == nil
}

// Encrypt seals plaintext with the unlocked key.
func (v *Vault) Encrypt(plaintext []byte) ([]byte, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.key == nil {
		return nil, ErrLocked
	}
	return sealWith(v.key, plaintext)
}

// Decrypt opens ciphertext with the unlocked key.
func (v *Vault) Decrypt(ciphertext []byte) ([]byte, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.key == nil {
		return nil, ErrLocked
	}
	return openWith(v.key, ciphertext)
}

func sealWith(key, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("read nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func openWith(key, ciphertext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < nonceLen {
		return nil, errors.New("ciphertext too short")
	}
	nonce, body := ciphertext[:nonceLen], ciphertext[nonceLen:]
	pt, err := gcm.Open(nil, nonce, body, nil)
	if err != nil {
		return nil, fmt.Errorf("gcm open: %w", err)
	}
	return pt, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	return gcm, nil
}
