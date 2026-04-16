package api

import "net/http"

func MeHandler(w http.ResponseWriter, r *http.Request) {
	claims, ok := AuthClaimsFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "missing auth context")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"user_id":    claims.UserID,
		"api_key_id": claims.APIKeyID,
		"subject":    claims.Subject,
		"expires_at": claims.ExpiresAt,
	})
}