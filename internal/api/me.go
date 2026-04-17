package api

import (
	"net/http"

	"github.com/oluu-web/lennut/internal/utils"
)

func MeHandler(w http.ResponseWriter, r *http.Request) {
	claims, ok := AuthClaimsFromContext(r.Context())
	if !ok {
		utils.WriteJSONError(w, http.StatusUnauthorized, "missing auth context")
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]any{
		"user_id":    claims.UserID,
		"api_key_id": claims.APIKeyID,
		"subject":    claims.Subject,
		"expires_at": claims.ExpiresAt,
	})
}