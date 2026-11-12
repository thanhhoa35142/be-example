package main

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/thanhhoa35142/be-example/internal/modules/apikeys"
)

type ctxKey string

const authContextKey ctxKey = "apikey.auth"

// authFrom returns the authenticated API key attached by apiKeyAuth.
func authFrom(ctx context.Context) (*apikeys.Auth, bool) {
	a, ok := ctx.Value(authContextKey).(*apikeys.Auth)
	return a, ok && a != nil
}

// apiKeyAuth requires "Authorization: Bearer phk_..." and resolves it to
// an apikeys.Auth (key + grants) stored in the request context.
func apiKeyAuth(svc *apikeys.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		token, ok := strings.CutPrefix(header, "Bearer ")
		if !ok || token == "" {
			writeAuthProblem(c, http.StatusUnauthorized, "Missing bearer token")
			return
		}
		auth, err := svc.Authenticate(c.Request.Context(), token)
		if err != nil {
			status := http.StatusUnauthorized
			if errors.Is(err, apikeys.ErrForbidden) {
				status = http.StatusForbidden
			}
			writeAuthProblem(c, status, "Invalid or expired API key")
			return
		}
		ctx := context.WithValue(c.Request.Context(), authContextKey, auth)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func writeAuthProblem(c *gin.Context, status int, detail string) {
	c.Header("WWW-Authenticate", `Bearer realm="pickhour-mcp"`)
	c.JSON(status, gin.H{
		"type":   "about:blank",
		"title":  http.StatusText(status),
		"status": status,
		"detail": detail,
	})
}

// requireScope wraps a tool call: resolves auth from ctx and enforces the
// (resource, action) grant before running fn.
func requireScope(ctx context.Context, svc *apikeys.Service, resource, action string) error {
	auth, ok := authFrom(ctx)
	if !ok {
		return errors.New("unauthenticated: missing API key context")
	}
	return svc.Authorize(auth, resource, action)
}
