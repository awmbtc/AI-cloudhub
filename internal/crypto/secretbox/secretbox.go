// Package secretbox provides envelope encryption for provider secrets at rest.
//
// Master key source (env AI_CLOUDHUB_MASTER_KEY):
//
//  1. Raw 32-byte key encoded as standard or raw Base64 (with or without padding).
//  2. Raw 32-byte key encoded as 64 hex characters (optionally 0x-prefixed).
//  3. Otherwise treated as a passphrase and derived with scrypt:
//
//     salt  = "ai-cloudhub-v1-master-key-salt"  (fixed, public; documented here)
//     N     = 1 << 15
//     r     = 8
//     p     = 1
//     keyLen= 32
//
// Ciphertext layout (versioned):
//
//	"ACH1" (4 bytes) || 24-byte nonce || nacl/secretbox ciphertext
//
// Dev mode: when AI_CLOUDHUB_MASTER_KEY is empty, callers should leave secrets
// in plaintext and log a warning. New("") returns an error; use FromEnv for
// the optional path.
package secretbox

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/crypto/nacl/secretbox"
	"golang.org/x/crypto/scrypt"
)

// EnvMasterKey is the environment variable holding the control-plane master key.
const EnvMasterKey = "AI_CLOUDHUB_MASTER_KEY"

// Fixed public salt for passphrase → key derivation (v1).
// Changing this invalidates all passphrase-derived keys.
const passphraseSalt = "ai-cloudhub-v1-master-key-salt"

const (
	magic     = "ACH1"
	magicLen  = 4
	nonceSize = 24
	keySize   = 32
	// minCiphertext: magic + nonce + secretbox overhead (16) + at least 0 plaintext
	overhead = magicLen + nonceSize + secretbox.Overhead
)

// Box holds a 32-byte NaCl secretbox key for Seal/Open.
type Box struct {
	key [keySize]byte
}

// New constructs a Box from a master key string.
// Accepts base64 (32 raw bytes), hex (32 raw bytes), or a passphrase (scrypt).
// Empty masterKey returns an error; use FromEnv for optional/dev mode.
func New(masterKey string) (*Box, error) {
	masterKey = strings.TrimSpace(masterKey)
	if masterKey == "" {
		return nil, errors.New("secretbox: empty master key (set AI_CLOUDHUB_MASTER_KEY)")
	}
	raw, err := parseMasterKey(masterKey)
	if err != nil {
		return nil, err
	}
	var b Box
	copy(b.key[:], raw)
	// Wipe stack copy best-effort
	for i := range raw {
		raw[i] = 0
	}
	return &b, nil
}

// FromEnv loads AI_CLOUDHUB_MASTER_KEY. Returns (nil, nil) when unset so callers
// can keep a plaintext/dev path. Returns a non-nil error only when the value is
// set but invalid.
func FromEnv() (*Box, error) {
	v := strings.TrimSpace(os.Getenv(EnvMasterKey))
	if v == "" {
		return nil, nil
	}
	return New(v)
}

// Seal encrypts plaintext. Each call uses a fresh random nonce.
func (b *Box) Seal(plaintext []byte) ([]byte, error) {
	if b == nil {
		return nil, errors.New("secretbox: nil Box")
	}
	var nonce [nonceSize]byte
	if _, err := io.ReadFull(rand.Reader, nonce[:]); err != nil {
		return nil, fmt.Errorf("secretbox: nonce: %w", err)
	}
	// out = magic || nonce || secretbox(plaintext)
	out := make([]byte, magicLen+nonceSize, magicLen+nonceSize+len(plaintext)+secretbox.Overhead)
	copy(out[:magicLen], magic)
	copy(out[magicLen:magicLen+nonceSize], nonce[:])
	return secretbox.Seal(out, plaintext, &nonce, &b.key), nil
}

// Open decrypts a Seal ciphertext. Returns an error on authentication failure
// or malformed input.
func (b *Box) Open(ciphertext []byte) ([]byte, error) {
	if b == nil {
		return nil, errors.New("secretbox: nil Box")
	}
	if len(ciphertext) < overhead {
		return nil, errors.New("secretbox: ciphertext too short")
	}
	if string(ciphertext[:magicLen]) != magic {
		return nil, errors.New("secretbox: unknown ciphertext version")
	}
	var nonce [nonceSize]byte
	copy(nonce[:], ciphertext[magicLen:magicLen+nonceSize])
	sealed := ciphertext[magicLen+nonceSize:]
	plain, ok := secretbox.Open(nil, sealed, &nonce, &b.key)
	if !ok {
		return nil, errors.New("secretbox: authentication failed")
	}
	return plain, nil
}

// parseMasterKey decodes base64/hex 32-byte keys or derives from passphrase.
func parseMasterKey(s string) ([]byte, error) {
	// 1) Hex (64 hex chars, optional 0x prefix)
	hexIn := s
	if strings.HasPrefix(strings.ToLower(hexIn), "0x") {
		hexIn = hexIn[2:]
	}
	if len(hexIn) == keySize*2 {
		if raw, err := hex.DecodeString(hexIn); err == nil && len(raw) == keySize {
			return raw, nil
		}
	}

	// 2) Base64 standard or raw (no padding)
	for _, dec := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		if raw, err := dec.DecodeString(s); err == nil && len(raw) == keySize {
			return raw, nil
		}
	}

	// 3) Passphrase → scrypt
	raw, err := scrypt.Key([]byte(s), []byte(passphraseSalt), 1<<15, 8, 1, keySize)
	if err != nil {
		return nil, fmt.Errorf("secretbox: scrypt: %w", err)
	}
	return raw, nil
}
