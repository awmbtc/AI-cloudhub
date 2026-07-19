package secretbox

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"os"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	// 32 zero bytes as base64
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	box, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("provider-secret-key-xyz")
	ct, err := box.Seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(ct, plain) {
		t.Fatal("ciphertext must differ from plaintext")
	}
	if !bytes.HasPrefix(ct, []byte(magic)) {
		t.Fatalf("missing magic prefix: %q", ct[:4])
	}
	got, err := box.Open(ct)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("got %q want %q", got, plain)
	}
}

func TestSealUniqueNonces(t *testing.T) {
	key := hex.EncodeToString(bytes.Repeat([]byte{0x11}, 32))
	box, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	a, err := box.Seal([]byte("same"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := box.Seal([]byte("same"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("expected different ciphertext for independent Seals")
	}
}

func TestOpenWrongKey(t *testing.T) {
	k1 := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	k2 := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32))
	b1, _ := New(k1)
	b2, _ := New(k2)
	ct, err := b1.Seal([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b2.Open(ct); err == nil {
		t.Fatal("expected auth failure with wrong key")
	}
}

func TestOpenTampered(t *testing.T) {
	key := hex.EncodeToString(bytes.Repeat([]byte{0xab}, 32))
	box, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := box.Seal([]byte("data"))
	if err != nil {
		t.Fatal(err)
	}
	ct[len(ct)-1] ^= 0xff
	if _, err := box.Open(ct); err == nil {
		t.Fatal("expected failure on tampered ciphertext")
	}
}

func TestOpenShort(t *testing.T) {
	key := hex.EncodeToString(bytes.Repeat([]byte{0xcd}, 32))
	box, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := box.Open([]byte("ACH1short")); err == nil {
		t.Fatal("expected short ciphertext error")
	}
}

func TestNewEmpty(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Fatal("expected error for empty master key")
	}
	if _, err := New("   "); err == nil {
		t.Fatal("expected error for whitespace master key")
	}
}

func TestParseHexAndBase64(t *testing.T) {
	raw := bytes.Repeat([]byte{0x7e}, 32)
	hexKey := hex.EncodeToString(raw)
	b64Key := base64.StdEncoding.EncodeToString(raw)
	b64Raw := base64.RawStdEncoding.EncodeToString(raw)

	for _, k := range []string{hexKey, "0x" + hexKey, b64Key, b64Raw} {
		box, err := New(k)
		if err != nil {
			t.Fatalf("New(%q): %v", k, err)
		}
		ct, err := box.Seal([]byte("x"))
		if err != nil {
			t.Fatal(err)
		}
		pt, err := box.Open(ct)
		if err != nil || string(pt) != "x" {
			t.Fatalf("round-trip failed for key form %q: %v %q", k, err, pt)
		}
	}
}

func TestPassphraseDeterministic(t *testing.T) {
	// Same passphrase must derive the same key (fixed salt documented in package).
	a, err := New("my-dev-passphrase-not-a-raw-key")
	if err != nil {
		t.Fatal(err)
	}
	b, err := New("my-dev-passphrase-not-a-raw-key")
	if err != nil {
		t.Fatal(err)
	}
	ct, err := a.Seal([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	pt, err := b.Open(ct)
	if err != nil || string(pt) != "hello" {
		t.Fatalf("passphrase-derived keys must match: %v %q", err, pt)
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv(EnvMasterKey, "")
	box, err := FromEnv()
	if err != nil || box != nil {
		t.Fatalf("empty env: got box=%v err=%v", box, err)
	}

	raw := bytes.Repeat([]byte{9}, 32)
	t.Setenv(EnvMasterKey, hex.EncodeToString(raw))
	box, err = FromEnv()
	if err != nil || box == nil {
		t.Fatalf("set env: got box=%v err=%v", box, err)
	}
	ct, _ := box.Seal([]byte("z"))
	pt, err := box.Open(ct)
	if err != nil || string(pt) != "z" {
		t.Fatal("FromEnv box failed round-trip")
	}

	// restore not needed: t.Setenv cleans up
	_ = os.Getenv
}

func TestNilBox(t *testing.T) {
	var b *Box
	if _, err := b.Seal([]byte("a")); err == nil {
		t.Fatal("expected nil Seal error")
	}
	if _, err := b.Open([]byte("ACH1xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")); err == nil {
		t.Fatal("expected nil Open error")
	}
}
