package serverapi

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"net/http"
)

func RegisterToolsAPI(mux *http.ServeMux) {
	mux.HandleFunc("/v1/tools/reality-keypair", handleRealityKeypair)
}

// handleRealityKeypair 生成 Reality 所需的 X25519 密钥对和 Short ID。
func handleRealityKeypair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	var sidBytes [8]byte
	if _, err := rand.Read(sidBytes[:]); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"private_key": base64.RawURLEncoding.EncodeToString(key.Bytes()),
		"public_key":  base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()),
		"short_id":    hex.EncodeToString(sidBytes[:]),
	})
}
