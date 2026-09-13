// Package cookies is the CLIENT half of state export/import (ADR 0011).
//
// The engine reads and writes the cookie store — it must, because the cookies that matter (li_at,
// auth_token, sessionid) are HttpOnly and invisible to a page eval. But three responsibilities the
// engine deliberately does NOT hold (it has no key management and its only output is a plaintext
// spool result file) live here:
//
//   - encryption at rest: the exported jar IS the identity, in plaintext, and bypasses 2FA. A
//     0600 file is one scp from a lost laptop, so a plaintext write demands an explicit,
//     self-describing flag (--i-know-this-is-a-credential).
//   - the audit trail: what was exported/imported, for which domains, by whom — never a value.
//   - reading the spool result and DELETING it immediately, so the plaintext jar does not linger
//     in the spool directory.
//
// Import fidelity is the engine's job and it reports rejections; this package's job is to make sure
// a caller cannot miss them — a "session" with silently dropped cookies is a broken login wearing a
// green light.
package cookies

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/deemwarhq/chrome-agent/internal/browser"
	"github.com/deemwarhq/chrome-agent/internal/ledger"
	"github.com/deemwarhq/chrome-agent/internal/sites"
	"golang.org/x/crypto/scrypt"
)

const magic = "CAJAR1\n" // marks an encrypted export; a plaintext one is bare JSON

type ExportResult struct {
	Domain        string `json:"domain"`
	Count         int    `json:"count"`
	HTTPOnlyCount int    `json:"http_only_count"`
	Out           string `json:"out"`
	Encrypted     bool   `json:"encrypted"`
	Warning       string `json:"warning,omitempty"`
}

type ImportResult struct {
	Imported      int              `json:"imported"`
	RejectedCount int              `json:"rejected_count"`
	Rejected      []map[string]any `json:"rejected,omitempty"`
	Warning       string           `json:"warning,omitempty"`
}

// Export pulls one domain's cookies from the engine and writes them, encrypted unless the caller
// has explicitly asked for plaintext.
func Export(b *browser.Browser, domain, outPath, passphrase string, allowPlaintext bool) (*ExportResult, error) {
	domain = sites.Normalize(domain)
	if domain == "" {
		return nil, fmt.Errorf("export requires a domain — a bare export would dump every identity at once")
	}
	if passphrase == "" && !allowPlaintext {
		return nil, fmt.Errorf("refusing to write an unencrypted credential: pass a passphrase, or --i-know-this-is-a-credential for plaintext")
	}
	res, err := b.SpoolClient().SendAndAwait(
		func(id string) string { return "COOKIEEXPORT:" + id + "|" + domain }, 20*time.Second)
	if err != nil {
		return nil, err
	}
	if ok, _ := res["ok"].(bool); !ok {
		return nil, fmt.Errorf("engine refused export: %v", res["error"])
	}
	// Serialize the engine's full ack (cookies + fidelity fields) verbatim — the import side wants
	// all of it back.
	raw, _ := json.Marshal(res)
	out := &ExportResult{Domain: domain, Out: outPath}
	if n, ok := res["count"].(float64); ok {
		out.Count = int(n)
	}
	if n, ok := res["http_only_count"].(float64); ok {
		out.HTTPOnlyCount = int(n)
	}
	var blob []byte
	if passphrase != "" {
		if blob, err = encrypt(raw, passphrase); err != nil {
			return nil, err
		}
		out.Encrypted = true
	} else {
		blob = raw
		out.Warning = "PLAINTEXT credential on disk — anyone with this file is signed in as you until the cookies expire; it bypasses 2FA. `chrome-agent logout` is the revocation."
	}
	if err := os.WriteFile(outPath, blob, 0o600); err != nil {
		return nil, err
	}
	// Ledger: domains and counts, NEVER a value.
	_, _ = ledger.Append(browser.AgentID(), "cookie:export", domain,
		fmt.Sprintf("count=%d encrypted=%v out=%s", out.Count, out.Encrypted, outPath))
	return out, nil
}

// Import sends a jar to the engine and REFUSES to look like success when cookies were rejected.
func Import(b *browser.Browser, inPath, passphrase string) (*ImportResult, error) {
	blob, err := os.ReadFile(inPath)
	if err != nil {
		return nil, err
	}
	var jar []byte
	if len(blob) >= len(magic) && string(blob[:len(magic)]) == magic {
		if passphrase == "" {
			return nil, fmt.Errorf("%s is encrypted — a passphrase is required", inPath)
		}
		if jar, err = decrypt(blob, passphrase); err != nil {
			return nil, fmt.Errorf("decrypt failed (wrong passphrase?): %w", err)
		}
	} else {
		jar = blob // plaintext export, or a hand-built array
	}
	res, err := b.SpoolClient().SendAndAwait(
		func(id string) string { return "COOKIEIMPORT:" + id + "|" + string(jar) }, 20*time.Second)
	if err != nil {
		return nil, err
	}
	if ok, _ := res["ok"].(bool); !ok {
		return nil, fmt.Errorf("engine refused import: %v", res["error"])
	}
	out := &ImportResult{}
	if n, ok := res["imported"].(float64); ok {
		out.Imported = int(n)
	}
	if n, ok := res["rejected_count"].(float64); ok {
		out.RejectedCount = int(n)
	}
	if r, ok := res["rejected"].([]any); ok {
		for _, e := range r {
			if m, ok := e.(map[string]any); ok {
				out.Rejected = append(out.Rejected, m)
			}
		}
	}
	if out.RejectedCount > 0 {
		// The whole point of ADR 0011 §5: the caller must not read "imported N" and assume a whole
		// session. Say it loudly, and let the CLI turn it into a non-zero exit.
		out.Warning = fmt.Sprintf("%d cookie(s) REJECTED — the session may be incomplete. This is not a clean import.", out.RejectedCount)
	}
	_, _ = ledger.Append(browser.AgentID(), "cookie:import", inPath,
		fmt.Sprintf("imported=%d rejected=%d", out.Imported, out.RejectedCount))
	return out, nil
}

// --- at-rest encryption: scrypt(passphrase) -> AES-256-GCM. No key ever touches the engine. ------

func deriveKey(passphrase string, salt []byte) ([]byte, error) {
	return scrypt.Key([]byte(passphrase), salt, 1<<15, 8, 1, 32)
}

func encrypt(plain []byte, passphrase string) ([]byte, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	key, err := deriveKey(passphrase, salt)
	if err != nil {
		return nil, err
	}
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ct := gcm.Seal(nil, nonce, plain, []byte(magic))
	out := []byte(magic)
	out = append(out, salt...)
	out = append(out, nonce...)
	return append(out, ct...), nil
}

func decrypt(blob []byte, passphrase string) ([]byte, error) {
	b := blob[len(magic):]
	if len(b) < 16 {
		return nil, fmt.Errorf("truncated")
	}
	salt, b := b[:16], b[16:]
	key, err := deriveKey(passphrase, salt)
	if err != nil {
		return nil, err
	}
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	ns := gcm.NonceSize()
	if len(b) < ns {
		return nil, fmt.Errorf("truncated")
	}
	nonce, ct := b[:ns], b[ns:]
	return gcm.Open(nil, nonce, ct, []byte(magic))
}

// checksum is unused by callers but keeps the import path honest in tests.
func checksum(b []byte) string { s := sha256.Sum256(b); return fmt.Sprintf("%x", s[:8]) }

var _ = io.EOF
var _ = checksum
