package cookies

import (
	"strings"
	"testing"
)

// The encryption is the whole point of ADR 0011 §3: the exported jar is a credential, so it must
// not be readable without the passphrase, and it must round-trip exactly.
func TestEncryptRoundTrips(t *testing.T) {
	plain := []byte(`{"ok":true,"domain":"linkedin.com","cookies":[{"name":"li_at","value":"SECRET"}]}`)
	blob, err := encrypt(plain, "correct horse battery staple")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !strings.HasPrefix(string(blob), magic) {
		t.Fatal("no magic header — import cannot tell encrypted from plaintext")
	}
	// The secret must not be sitting in the ciphertext.
	if strings.Contains(string(blob), "SECRET") {
		t.Fatal("cookie value is readable in the encrypted blob")
	}
	got, err := decrypt(blob, "correct horse battery staple")
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(got) != string(plain) {
		t.Fatal("round-trip corrupted the jar")
	}
}

func TestWrongPassphraseFails(t *testing.T) {
	blob, _ := encrypt([]byte(`{"cookies":[]}`), "right")
	if _, err := decrypt(blob, "wrong"); err == nil {
		t.Fatal("decrypt accepted the wrong passphrase")
	}
}

// A tampered byte must be caught: GCM authenticates, so a flipped bit in the ciphertext fails
// rather than yielding a subtly-wrong jar.
func TestTamperIsDetected(t *testing.T) {
	blob, _ := encrypt([]byte(`{"cookies":[{"name":"x"}]}`), "pw")
	blob[len(blob)-1] ^= 0x01
	if _, err := decrypt(blob, "pw"); err == nil {
		t.Fatal("a tampered blob decrypted without error")
	}
}
