package http

import (
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/platform/httpx"
)

// requestIDRe is the only shape of client-supplied X-Request-ID we echo into
// logs and responses: short and free of control chars / log-injection bytes.
var requestIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-ID")
		if !requestIDRe.MatchString(id) {
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

func Logger() gin.HandlerFunc {
	return gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		return time.Now().Format("2006/01/02 - 15:04:05") +
			" | " + param.Method +
			" | " + param.StatusCodeColor() + fmt.Sprint(param.StatusCode) + param.ResetColor() +
			" | " + param.Latency.String() +
			" | " + param.ClientIP +
			" | " + pathWithoutQuery(param.Path) + " | " + param.ErrorMessage + "\n"
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
		httpx.WriteProblem(c, http.StatusInternalServerError, httpx.CodeInternal, "Internal Server Error", "")
		c.Abort()
	})
}

// pathWithoutQuery drops the query string so identifiers/credentials passed
// as query parameters (legacy ?deviceId=) never reach the access log.
func pathWithoutQuery(p string) string {
	if i := strings.IndexByte(p, '?'); i >= 0 {
		return p[:i]
	}
	return p
}
