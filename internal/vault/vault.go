package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"io"
	"time"

	_ "modernc.org/sqlite"
)

// SecretVault provides AES-256-GCM encrypted secret storage backed by SQLite.
type SecretVault struct {
	db *sql.DB
}

func Open(dbPath string) (*SecretVault, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open vault db: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	v := &SecretVault{db: db}
	if err := v.ensureKey(); err != nil {
		db.Close()
		return nil, err
	}
	return v, nil
}

func (v *SecretVault) Close() error { return v.db.Close() }

func migrate(db *sql.DB) error {
	ddl := `
	CREATE TABLE IF NOT EXISTS keyring (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		key_b64url TEXT NOT NULL,
		created_at TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS secrets (
		name TEXT PRIMARY KEY,
		ciphertext BLOB NOT NULL,
		updated_at TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS audit_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		dal_id TEXT,
		action TEXT NOT NULL,
		result TEXT,
		actor  TEXT,
		timestamp TEXT NOT NULL
	);`
	_, err := db.Exec(ddl)
	return err
}

func (v *SecretVault) ensureKey() error {
	var exists int
	if err := v.db.QueryRow("SELECT COUNT(*) FROM keyring WHERE id=1").Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return nil
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return fmt.Errorf("generate key: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(key)
	_, err := v.db.Exec("INSERT INTO keyring(id,key_b64url,created_at) VALUES(1,?,?)",
		encoded, time.Now().UTC().Format(time.RFC3339))
	return err
}

func (v *SecretVault) loadKey() ([]byte, error) {
	var encoded string
	if err := v.db.QueryRow("SELECT key_b64url FROM keyring WHERE id=1").Scan(&encoded); err != nil {
		return nil, fmt.Errorf("load key: %w", err)
	}
	return base64.RawURLEncoding.DecodeString(encoded)
}

func (v *SecretVault) encrypt(plaintext []byte) ([]byte, error) {
	key, err := v.loadKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func (v *SecretVault) decrypt(ciphertext []byte) ([]byte, error) {
	key, err := v.loadKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(ciphertext) < ns {
		return nil, fmt.Errorf("ciphertext too short")
	}
	return gcm.Open(nil, ciphertext[:ns], ciphertext[ns:], nil)
}

func (v *SecretVault) Set(name string, value []byte) error {
	ct, err := v.encrypt(value)
	if err != nil {
		return err
	}
	_, err = v.db.Exec(
		`INSERT INTO secrets(name,ciphertext,updated_at) VALUES(?,?,?)
		 ON CONFLICT(name) DO UPDATE SET ciphertext=excluded.ciphertext, updated_at=excluded.updated_at`,
		name, ct, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return err
	}
	return v.audit("", "secret.set", name, "dalcenter")
}

func (v *SecretVault) Get(name string) ([]byte, error) {
	var ct []byte
	if err := v.db.QueryRow("SELECT ciphertext FROM secrets WHERE name=?", name).Scan(&ct); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("secret %q not found", name)
		}
		return nil, err
	}
	plain, err := v.decrypt(ct)
	if err != nil {
		return nil, err
	}
	_ = v.audit("", "secret.get", name, "dalcenter")
	return plain, nil
}

type SecretEntry struct {
	Name      string
	UpdatedAt string
}

func (v *SecretVault) List() ([]SecretEntry, error) {
	rows, err := v.db.Query("SELECT name, updated_at FROM secrets ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SecretEntry
	for rows.Next() {
		var e SecretEntry
		if err := rows.Scan(&e.Name, &e.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (v *SecretVault) audit(dalID, action, result, actor string) error {
	_, err := v.db.Exec(
		"INSERT INTO audit_events(dal_id,action,result,actor,timestamp) VALUES(?,?,?,?,?)",
		dalID, action, result, actor, time.Now().UTC().Format(time.RFC3339))
	return err
}
