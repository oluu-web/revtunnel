package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var ErrInvalidToken = errors.New("invalid token")

type Claims struct {
	UserID   string `json:"user_id"`
	APIKeyID string `json:"api_key_id"`
	jwt.RegisteredClaims
}

type TokenIssuer struct {
	issuer   string
	audience string
	secret   []byte
	ttl      time.Duration
}

func NewTokenIssuer(secret, issuer, audience string, ttl time.Duration) (*TokenIssuer, error) {
	if secret == "" {
		return nil, errors.New("token secret is required")
	}
	if issuer == "" {
		return nil, errors.New("token issuer is required")
	}
	if audience == "" {
		return nil, errors.New("token audience is required")
	}
	if ttl <= 0 {
		return nil, errors.New("token ttl must be positive")
	}

	return &TokenIssuer{
		issuer:   issuer,
		audience: audience,
		secret:   []byte(secret),
		ttl:      ttl,
	}, nil
}

func (t *TokenIssuer) Issue(userID, apiKeyID string) (string, time.Time, error) {
	if userID == "" {
		return "", time.Time{}, errors.New("user id is required")
	}
	if apiKeyID == "" {
		return "", time.Time{}, errors.New("api key id is required")
	}

	now := time.Now().UTC()
	expiresAt := now.Add(t.ttl)

	claims := Claims{
		UserID: userID,
		APIKeyID: apiKeyID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: t.issuer,
			Subject: userID,
			Audience: jwt.ClaimStrings{t.audience},
			IssuedAt: jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(t.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign token: %w", err)
	}
	return signed, expiresAt, nil
}

func (t *TokenIssuer) Parse(tokenString string) (*Claims, error) {
	claims := &Claims{}

	token, err := jwt.ParseWithClaims(
		tokenString,
		claims,
		func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, ErrInvalidToken
			}
			return t.secret, nil
		},
		jwt.WithIssuer(t.issuer),
		jwt.WithAudience(t.audience),
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if !token.Valid {
		return nil, ErrInvalidToken
	}

	return claims, nil
}