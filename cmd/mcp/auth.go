package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/thanhhoa35142/be-example/internal/modules/apikeys"
	"github.com/thanhhoa35142/be-example/internal/platform/ratelimit"
)

type ctxKey string

const authContextKey ctxKey = "apikey.auth"

func authFrom(ctx context.Context) (*apikeys.Auth, bool) {
	a, ok := ctx.Value(authContextKey).(*apikeys.Auth)
	return a, ok && a != nil
}

func apiKeyAuth(svc *apikeys.Service, limiter *keyLimiter, failed *ratelimit.Limiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		token, ok := strings.CutPrefix(header, "Bearer ")
		if !ok || token == "" {
			writeAuthProblem(c, http.StatusUnauthorized, "Missing bearer token")
			return
		}
		ip := c.ClientIP()
		if denied, retry := failed.Deny(ip); denied {
			c.Header("Retry-After", strconv.Itoa(int(retry/time.Second)))
			writeAuthProblem(c, http.StatusTooManyRequests, "Too many failed authentication attempts")
			return
		}
		auth, err := svc.Authenticate(c.Request.Context(), token)
		if err != nil {
			failed.Hit(ip)
			status := http.StatusUnauthorized
			if errors.Is(err, apikeys.ErrForbidden) {
				status = http.StatusForbidden
			}
			writeAuthProblem(c, status, "Invalid or expired API key")
			return
		}
		if retryAfter := limiter.retryAfter(auth.Key); retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
			writeAuthProblem(c, http.StatusTooManyRequests, "Rate limit exceeded")
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

func requireScope(ctx context.Context, svc *apikeys.Service, resource, action string) error {
	auth, ok := authFrom(ctx)
	if !ok {
		return errors.New("unauthenticated: missing API key context")
	}
	return svc.Authorize(auth, resource, action)
}
