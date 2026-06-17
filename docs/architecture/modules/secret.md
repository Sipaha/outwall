# module: internal/secret

The master-password vault. Derives a key with Argon2id (`time=1, memory=64 MiB,
threads=4, keyLen=32`) from the master password plus a stored random salt, and uses it for
per-secret AES-256-GCM (12-byte random nonce, stored as `nonce || ciphertext`). The derived
key lives only in memory while unlocked; the password is never stored. A fixed verifier
plaintext is sealed at init and re-opened at unlock to validate the password.

## Public API

- `NewVault(s *store.Store) *Vault`
- `(*Vault).Initialized() (bool, error)`
- `(*Vault).Init(password string) error` — first-time setup; leaves the vault unlocked.
- `(*Vault).Unlock(password string) error` — `ErrNotInitialized`, `ErrBadPassword`.
- `(*Vault).Lock()` — zeroes the in-memory key.
- `(*Vault).Locked() bool`
- `(*Vault).Encrypt([]byte) ([]byte, error)` / `(*Vault).Decrypt([]byte) ([]byte, error)` — `ErrLocked` when locked.
- Errors: `ErrNotInitialized`, `ErrBadPassword`, `ErrLocked`.
