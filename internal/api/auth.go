package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/oluu-web/lennut/internal/apikeys"
	"github.com/oluu-web/lennut/internal/auth"
)

type AuthHandler struct {
	DB *sql.DB
	Tokens *auth.TokenIssuer
}

type authTokenRequest struct {
	APIKey string `json:"api_key"`
}

type authTokenResponse struct {
	Token      string `json:"token"`
	TTLSeconds int64  `json:"ttl_seconds"`
}

type apiKeyRow struct {
	ID        string
	UserID    string
	KeyHash   string
	RevokedAt sql.NullTime
}

func (h *AuthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req authTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	prefix, err := apikeys.ParsePrefix(req.APIKey)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "invalid api key")
		return
	}

	row, err := h.lookupAPIKey(r, prefix)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, http.StatusUnauthorized, "invalid api key")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if row.RevokedAt.Valid {
		writeJSONError(w, http.StatusUnauthorized, "api key revoked")
		return
	}

	if !apikeys.Verify(req.APIKey, row.KeyHash) {
		writeJSONError(w, http.StatusUnauthorized, "invalid api key")
		return
	}

	if err := h.touchAPIKey(r, row.ID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	token, expiresAt, err := h.Tokens.Issue(row.UserID, row.ID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to issue token")
		return
	}

	resp := authTokenResponse{
		Token:      token,
		TTLSeconds: int64(time.Until(expiresAt).Seconds()),
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *AuthHandler) lookupAPIKey(r *http.Request, prefix string) (*apiKeyRow, error) {
	row := h.DB.QueryRowContext(
		r.Context(),
		`
		SELECT id, user_id, key_hash, revoked_at
		FROM api_keys
		WHERE key_prefix = $1
		`,
		prefix,
	)

	var out apiKeyRow
	if err := row.Scan(&out.ID, &out.UserID, &out.KeyHash, &out.RevokedAt); err != nil {
		return nil, err
	}
	return &out, nil
}

func (h *AuthHandler) touchAPIKey(r *http.Request, apiKeyID string) error {
	_, err := h.DB.ExecContext(
		r.Context(),
		`
		UPDATE api_keys
		SET last_used_at = now()
		WHERE id = $1
		`,
		apiKeyID,
	)
	return err
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{
		"error": message,
	})
}