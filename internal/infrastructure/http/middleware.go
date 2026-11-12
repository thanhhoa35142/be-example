package http

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-ID")
		if id == "" {
			id = uuid.NewString()
		}
		c.Set("requestID", id)
		c.Header("X-Request-ID", id)
		c.Next()
	}
}

// ClientInfo captures client-identification headers (X-Client,
// X-Client-Version, User-Agent) into the gin context for logging.
func ClientInfo() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("client", c.GetHeader("X-Client"))
		c.Set("clientVersion", c.GetHeader("X-Client-Version"))
		c.Set("userAgent", c.GetHeader("User-Agent"))
		c.Next()
	}
}

// AdminAuth guards staff endpoints with a bearer token from ADMIN_TOKEN.
// When ADMIN_TOKEN is unset the admin surface is disabled entirely (404).
func AdminAuth() gin.HandlerFunc {
	token := os.Getenv("ADMIN_TOKEN")
	return func(c *gin.Context) {
		if token == "" {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		if c.GetHeader("Authorization") != "Bearer "+token {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	}
}

func Logger() gin.HandlerFunc {
	return gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		return time.Now().Format("2006/01/02 - 15:04:05") +
			" | " + param.Method +
			" | " + param.StatusCodeColor() + fmt.Sprint(param.StatusCode) + param.ResetColor() +
			" | " + param.Latency.String() +
			" | " + param.ClientIP +
			" | " + param.Path + " | " + param.ErrorMessage + "\n"
	})
}

// Recovery replaces gin.Recovery with one that logs the panic + stack via
// slog so crashes are debuggable instead of silent 500s.
func Recovery() gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered any) {
		slog.ErrorContext(c.Request.Context(), "panic recovered",
			"request_id", c.GetString("requestID"),
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"panic", fmt.Sprint(recovered),
			"stack", string(debug.Stack()),
		)
		c.AbortWithStatusJSON(500, gin.H{"title": "Internal Server Error", "status": 500})
	})
}
