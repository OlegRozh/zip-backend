// Package middleware contains HTTP middleware helpers.
package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/reqctx"
)

const bearerPrefix = "Bearer "

// AuthMiddleware validates Bearer HS256 JWT and stores authenticated user ID in request context.
func AuthMiddleware(secret string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, ok := authenticateBearer(r.Header.Get("Authorization"), secret)
			if !ok {
				sendJSONError(w, apperr.ErrUnauthorized, GetRequestID(r.Context()))
				return
			}

			ctx := reqctx.PutUserID(r.Context(), userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func authenticateBearer(header string, secret string) (string, bool) {
	var userID string
	ok := false
	if strings.HasPrefix(header, bearerPrefix) && secret != "" {
		token := strings.TrimSpace(strings.TrimPrefix(header, bearerPrefix))
		claims, valid := validateHS256JWT(token, secret)
		if valid {
			userID, ok = userIDFromClaims(claims)
		}
	}
	return userID, ok
}

func validateHS256JWT(token string, secret string) (map[string]any, bool) {
	claims := map[string]any{}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return claims, false
	}
	if !validJWTSignature(parts, secret) {
		return claims, false
	}

	header, ok := decodeJWTPart(parts[0])
	if !ok || header["alg"] != "HS256" {
		return claims, false
	}

	payload, ok := decodeJWTPart(parts[1])
	if !ok || tokenExpired(payload) {
		return claims, false
	}
	return payload, true
}

func validJWTSignature(parts []string, secret string) bool {
	signed := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signed))
	expected := mac.Sum(nil)
	actual, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	return hmac.Equal(actual, expected)
}

func decodeJWTPart(part string) (map[string]any, bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(part)
	if err != nil {
		return nil, false
	}
	var payload map[string]any
	if err := json.Unmarshal(decoded, &payload); err != nil {
		return nil, false
	}
	return payload, true
}

func tokenExpired(claims map[string]any) bool {
	exp, ok := claims["exp"].(float64)
	return ok && time.Now().Unix() >= int64(exp)
}

func userIDFromClaims(claims map[string]any) (string, bool) {
	for _, claimName := range []string{"sub", "user_id", "userId", "uid", "id"} {
		userID, ok := claims[claimName].(string)
		if ok && userID != "" {
			if _, err := uuid.Parse(userID); err == nil {
				return userID, true
			}
		}
	}
	return "", false
}
