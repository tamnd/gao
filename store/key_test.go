package store

import (
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAKeyPairRoundTripsThroughFiles(t *testing.T) {
	dir := t.TempDir()
	pub, priv, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	privPath := filepath.Join(dir, "gao.key")
	pubPath := filepath.Join(dir, "gao.pub")

	if err := WritePrivateKey(privPath, priv); err != nil {
		t.Fatalf("WritePrivateKey: %v", err)
	}
	if err := WritePublicKey(pubPath, pub); err != nil {
		t.Fatalf("WritePublicKey: %v", err)
	}

	back, err := LoadPrivateKey(privPath)
	if err != nil {
		t.Fatalf("LoadPrivateKey: %v", err)
	}
	if !back.Equal(priv) {
		t.Error("the private key changed on the way through a file")
	}
	backPub, err := LoadPublicKey(pubPath)
	if err != nil {
		t.Fatalf("LoadPublicKey: %v", err)
	}
	if !backPub.Equal(pub) {
		t.Error("the public key changed on the way through a file")
	}

	// The public key file is one line of hex, which is the whole point of the
	// format: somebody can paste it into a release note and a verifier written
	// in any language can use it.
	raw, err := os.ReadFile(pubPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(raw)); len(got) != 2*ed25519.PublicKeySize {
		t.Errorf("the public key file is %d characters, want %d", len(got), 2*ed25519.PublicKeySize)
	}

	parsed, err := ParsePublicKey(string(raw))
	if err != nil {
		t.Fatalf("ParsePublicKey: %v", err)
	}
	if !parsed.Equal(pub) {
		t.Error("ParsePublicKey did not return the key that was written")
	}
}

func TestWritingAKeyRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gao.key")
	_, priv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := WritePrivateKey(path, priv); err != nil {
		t.Fatalf("WritePrivateKey: %v", err)
	}
	_, other, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := WritePrivateKey(path, other); !errors.Is(err, os.ErrExist) {
		t.Fatalf("WritePrivateKey over an existing key = %v, want os.ErrExist", err)
	}
	back, err := LoadPrivateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if !back.Equal(priv) {
		t.Fatal("the original signing key was replaced")
	}
}

func TestAWorldReadableSigningKeyIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the permission bits are a fiction on Windows and the check is skipped there")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "gao.key")
	_, priv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := WritePrivateKey(path, priv); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPrivateKey(path); !errors.Is(err, ErrBadKey) {
		t.Fatalf("LoadPrivateKey on a 0644 key = %v, want ErrBadKey", err)
	}
}

func TestMalformedKeysAreRefused(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"empty":                                 "",
		"not hex":                               "the quick brown fox",
		"too short":                             "00ff",
		"too long":                              strings.Repeat("ab", ed25519.PublicKeySize+1),
		"a private key where a public key goes": strings.Repeat("cd", ed25519.PrivateKeySize),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".pub")
			if err := os.WriteFile(path, []byte(body+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadPublicKey(path); !errors.Is(err, ErrBadKey) {
				t.Fatalf("LoadPublicKey = %v, want ErrBadKey", err)
			}
			if _, err := ParsePublicKey(body); !errors.Is(err, ErrBadKey) {
				t.Fatalf("ParsePublicKey = %v, want ErrBadKey", err)
			}
		})
	}
}

func TestLoadReportsAMissingKeyFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nothing.key")
	if _, err := LoadPrivateKey(missing); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("LoadPrivateKey = %v, want os.ErrNotExist", err)
	}
	if _, err := LoadPublicKey(missing); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("LoadPublicKey = %v, want os.ErrNotExist", err)
	}
}

// A key loaded from disk signs a manifest that verifies against the public key
// loaded from disk, which is the only property any of this exists for.
func TestAKeyFromDiskSignsAManifestThatVerifies(t *testing.T) {
	dir := t.TempDir()
	pub, priv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	privPath, pubPath := filepath.Join(dir, "gao.key"), filepath.Join(dir, "gao.pub")
	if err := WritePrivateKey(privPath, priv); err != nil {
		t.Fatal(err)
	}
	if err := WritePublicKey(pubPath, pub); err != nil {
		t.Fatal(err)
	}

	signer, err := LoadPrivateKey(privPath)
	if err != nil {
		t.Fatal(err)
	}
	m := manifest(4)
	if err := m.Seal(signer, sealedAt); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	verifier, err := LoadPublicKey(pubPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.VerifySignature(); err != nil {
		t.Fatalf("VerifySignature: %v", err)
	}
	got, err := m.SignerKey()
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(verifier) {
		t.Fatal("the manifest was not signed by the key on disk")
	}
}
