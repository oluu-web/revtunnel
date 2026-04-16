package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/oluu-web/lennut/internal/auth"
)

type contextKey struct {
	name string
}

var claimsContextKey = &contextKey{name: "auth-claims"}

func RequireJWT(tokens *auth.TokenIssuer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenString, ok := bearerToken(r.Header.Get("Authorization"))
			if !ok {
				writeJSONError(w, http.StatusUnauthorized, "missing bearer token")
				return
			}

			claims, err := tokens.Parse(tokenString)
			if err != nil {
				writeJSONError(w, http.StatusUnauthorized, "invalid token")
				return
			}

			ctx := context.WithValue(r.Context(), claimsContextKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func AuthClaimsFromContext(ctx context.Context) (*auth.Claims, bool) {
	claims, ok := ctx.Value(claimsContextKey).(*auth.Claims)
	return claims, ok
}

func bearerToken(header string) (string, bool) {
	if header == "" {
		return "", false
	}

	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 {
		return "", false
	}
	if !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	if strings.TrimSpace(parts[1]) == "" {
		return "", false
	}

	return parts[1], true
}