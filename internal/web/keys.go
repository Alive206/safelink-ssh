package web

import (
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/crypto/ssh"
)

// keyMaxBytes caps an uploaded private key at 64 KiB — far larger than any
// real OpenSSH/PEM key but small enough that a malicious client can't
// fill the disk with one request.
const keyMaxBytes = 64 * 1024

// safeKeyName restricts uploaded filenames to a small whitelist so they
// can't escape the keys directory or shadow system files.
var safeKeyName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// KeyInfo is the JSON shape returned by GET /api/keys.
type KeyInfo struct {
	Name        string `json:"name"`         // base filename only
	Path        string `json:"path"`         // absolute path the daemon will use
	Size        int64  `json:"size"`         // bytes
	Fingerprint string `json:"fingerprint"`  // sha256 of the public key, when parseable
	HasPassword bool   `json:"has_password"` // true if the private key is encrypted
	InUse       bool   `json:"in_use"`       // referenced by any tunnel
}

// listKeys returns every readable file in the keys directory.
func (h *handler) listKeys(w http.ResponseWriter, _ *http.Request) {
	dir := h.mgr.KeysDir()
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		writeErr(w, http.StatusInternalServerError, "read keys dir: "+err.Error())
		return
	}
	out := make([]KeyInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !safeKeyName.MatchString(e.Name()) {
			continue
		}
		full := filepath.Join(dir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		ki := KeyInfo{Name: e.Name(), Path: full, Size: info.Size()}
		if data, err := os.ReadFile(full); err == nil {
			ki.Fingerprint, ki.HasPassword = inspectKey(data)
		}
		ki.InUse = h.mgr.IsKeyInUse(full)
		out = append(out, ki)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, out)
}

// uploadKey accepts a multipart upload (`file` field, optional `name` field)
// and writes it into the keys directory with 0600 permissions.  Re-uploading
// a name overwrites in place so the user can rotate a key without breaking
// the tunnel that references its path.
func (h *handler) uploadKey(w http.ResponseWriter, r *http.Request) {
	// Reject obviously oversized requests up front.
	r.Body = http.MaxBytesReader(w, r.Body, keyMaxBytes+8*1024)
	if err := r.ParseMultipartForm(keyMaxBytes + 8*1024); err != nil {
		writeErr(w, http.StatusBadRequest, "multipart parse: "+err.Error())
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "missing 'file' field")
		return
	}
	defer file.Close()

	// Prefer an explicit `name=` field if provided; otherwise sanitise the
	// uploaded filename.  Either way the result must match safeKeyName.
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = filepath.Base(hdr.Filename)
	}
	name = strings.ReplaceAll(name, " ", "_")
	if !safeKeyName.MatchString(name) {
		writeErr(w, http.StatusBadRequest, "invalid key name (allowed: letters, digits, . _ -, max 64 chars)")
		return
	}

	data, err := io.ReadAll(io.LimitReader(file, keyMaxBytes+1))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read upload: "+err.Error())
		return
	}
	if len(data) > keyMaxBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("key exceeds %d bytes", keyMaxBytes))
		return
	}
	if !looksLikePrivateKey(data) {
		writeErr(w, http.StatusBadRequest, "file does not look like a PEM/OpenSSH private key")
		return
	}

	dir := h.mgr.KeysDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		writeErr(w, http.StatusInternalServerError, "mkdir keys: "+err.Error())
		return
	}
	full := filepath.Join(dir, name)

	// Atomic-ish write: temp file in same dir, fsync, rename.  Permissions
	// are tightened to 0600 so the key isn't world-readable.
	tmp, err := os.CreateTemp(dir, ".upload-*.key")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "create tmp: "+err.Error())
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		writeErr(w, http.StatusInternalServerError, "write tmp: "+err.Error())
		return
	}
	_ = tmp.Sync()
	_ = tmp.Close()
	_ = os.Chmod(tmpName, 0o600)
	if err := os.Rename(tmpName, full); err != nil {
		writeErr(w, http.StatusInternalServerError, "rename: "+err.Error())
		return
	}

	fp, hasPw := inspectKey(data)
	writeJSON(w, http.StatusCreated, KeyInfo{
		Name:        name,
		Path:        full,
		Size:        int64(len(data)),
		Fingerprint: fp,
		HasPassword: hasPw,
	})
}

// deleteKey removes a key by basename.  Refuses if any tunnel still
// references the file.
func (h *handler) deleteKey(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !safeKeyName.MatchString(name) {
		writeErr(w, http.StatusBadRequest, "invalid key name")
		return
	}
	full := filepath.Join(h.mgr.KeysDir(), name)
	if h.mgr.IsKeyInUse(full) {
		writeErr(w, http.StatusConflict, "key is in use by a tunnel; detach it first")
		return
	}
	if err := os.Remove(full); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeErr(w, http.StatusNotFound, "no such key")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// looksLikePrivateKey is a fast, format-agnostic sniff so we reject obvious
// non-keys before writing them to disk.  It accepts both PEM blocks and the
// modern OPENSSH key wrapper.
func looksLikePrivateKey(data []byte) bool {
	s := string(data)
	switch {
	case strings.Contains(s, "-----BEGIN OPENSSH PRIVATE KEY-----"):
		return true
	case strings.Contains(s, "-----BEGIN RSA PRIVATE KEY-----"):
		return true
	case strings.Contains(s, "-----BEGIN EC PRIVATE KEY-----"):
		return true
	case strings.Contains(s, "-----BEGIN DSA PRIVATE KEY-----"):
		return true
	case strings.Contains(s, "-----BEGIN PRIVATE KEY-----"):
		return true
	case strings.Contains(s, "-----BEGIN ENCRYPTED PRIVATE KEY-----"):
		return true
	}
	return false
}

// inspectKey extracts a public-key fingerprint and an encrypted/clear flag
// when the data looks like a recognisable PEM/OpenSSH key.  Best effort:
// returns ("", false) on any parse failure.
func inspectKey(data []byte) (fingerprint string, encrypted bool) {
	// Detect PEM-level encryption flag without parsing.
	if block, _ := pem.Decode(data); block != nil {
		if _, ok := block.Headers["DEK-Info"]; ok {
			encrypted = true
		}
		// PKCS#8 encrypted body has a different OID; we don't decode it.
		if block.Type == "ENCRYPTED PRIVATE KEY" {
			encrypted = true
		}
	}

	if signer, err := ssh.ParsePrivateKey(data); err == nil {
		fingerprint = ssh.FingerprintSHA256(signer.PublicKey())
		return
	}
	// If parsing failed because the key is encrypted, that itself is signal.
	if _, err := ssh.ParseRawPrivateKey(data); err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "passphrase") || strings.Contains(msg, "encrypted") {
			encrypted = true
		}
	}
	return
}
