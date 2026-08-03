package kho

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
)

// Signing keys are ed25519 and they are stored as one line of hex.
//
// Not PEM, not PKCS8, not an age or SSH key format. A snapshot signature has one
// job, which is to let somebody who has our public key tell whether the corpus
// they downloaded is the corpus we published, and the fewer moving parts between
// them and that answer the better. A verifier can be written in ten lines in any
// language against a 64 character hex string.

// ErrBadKey is returned when a key file is not a gao signing key.
var ErrBadKey = errors.New("kho: not a gao signing key")

// GenerateKey returns a new signing key.
func GenerateKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("kho: generating a signing key: %w", err)
	}
	return pub, priv, nil
}

// WritePrivateKey writes a private key to path with owner-only permissions.
func WritePrivateKey(path string, key ed25519.PrivateKey) error {
	if len(key) != ed25519.PrivateKeySize {
		return fmt.Errorf("%w: private key is %d bytes, want %d", ErrBadKey, len(key), ed25519.PrivateKeySize)
	}
	return writeKeyFile(path, key, 0o600)
}

// WritePublicKey writes a public key to path.
func WritePublicKey(path string, key ed25519.PublicKey) error {
	if len(key) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: public key is %d bytes, want %d", ErrBadKey, len(key), ed25519.PublicKeySize)
	}
	return writeKeyFile(path, key, 0o644)
}

func writeKeyFile(path string, key []byte, mode os.FileMode) error {
	// O_EXCL, because the failure mode of a key writer that overwrites is
	// generating a fresh key over the one every published snapshot was signed
	// with, and noticing at the next release.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("kho: writing %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := fmt.Fprintln(f, hex.EncodeToString(key)); err != nil {
		return fmt.Errorf("kho: writing %s: %w", path, err)
	}
	return f.Close()
}

// LoadPrivateKey reads a signing key.
//
// It refuses to read a key file that anybody but its owner can read. That check
// is skipped on Windows, where the permission bits are a fiction and the real
// answer lives in an access control list this package is not going to parse.
func LoadPrivateKey(path string) (ed25519.PrivateKey, error) {
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("kho: reading %s: %w", path, err)
		}
		if mode := info.Mode().Perm(); mode&0o077 != 0 {
			return nil, fmt.Errorf("%w: %s is mode %#o and a signing key must be 0600", ErrBadKey, path, mode)
		}
	}
	b, err := readKeyFile(path, ed25519.PrivateKeySize)
	if err != nil {
		return nil, err
	}
	return ed25519.PrivateKey(b), nil
}

// LoadPublicKey reads a verifying key.
func LoadPublicKey(path string) (ed25519.PublicKey, error) {
	b, err := readKeyFile(path, ed25519.PublicKeySize)
	if err != nil {
		return nil, err
	}
	return ed25519.PublicKey(b), nil
}

// ParsePublicKey parses a hex-encoded verifying key, which is the form it takes
// on a command line and in a release note.
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	b, err := decodeKey(strings.TrimSpace(s), ed25519.PublicKeySize)
	if err != nil {
		return nil, err
	}
	return ed25519.PublicKey(b), nil
}

func readKeyFile(path string, size int) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("kho: reading %s: %w", path, err)
	}
	b, err := decodeKey(strings.TrimSpace(string(raw)), size)
	if err != nil {
		return nil, fmt.Errorf("%w: in %s", err, path)
	}
	return b, nil
}

func decodeKey(s string, size int) ([]byte, error) {
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("%w: expected hex", ErrBadKey)
	}
	if len(b) != size {
		return nil, fmt.Errorf("%w: got %d bytes, want %d", ErrBadKey, len(b), size)
	}
	return b, nil
}
