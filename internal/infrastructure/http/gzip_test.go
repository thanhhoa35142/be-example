package http

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func gzipRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Gzip())
	r.GET("/v1/spots", func(c *gin.Context) {
		c.JSON(200, gin.H{"data": gin.H{"items": make([]int, 1000)}})
	})
	return r
}

func TestGzipCompressesJSON(t *testing.T) {
	r := gzipRouter()
	w := do(r, "GET", "/v1/spots", "1.2.3.4", map[string]string{"Accept-Encoding": "gzip"}, "")
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	if got := w.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding=%q want gzip", got)
	}
	if got := w.Header().Get("Vary"); got != "Accept-Encoding" {
		t.Fatalf("Vary=%q want Accept-Encoding", got)
	}
	zr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatalf("body is not gzip: %v", err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	if len(raw) == 0 || w.Body.Len() >= len(raw) {
		t.Fatalf("expected compression, got %d -> %d bytes", len(raw), w.Body.Len())
	}
}

func TestGzipPassesThroughWithoutAcceptEncoding(t *testing.T) {
	r := gzipRouter()
	w := do(r, "GET", "/v1/spots", "1.2.3.4", nil, "")
	if got := w.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("unexpected Content-Encoding %q", got)
	}
	if w.Body.Len() == 0 {
		t.Fatal("empty body")
	}
}

func TestGzipSkipsNonJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Gzip())
	r.GET("/s/:id", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte("<html>hi</html>"))
	})
	w := do(r, "GET", "/s/x", "1.2.3.4", map[string]string{"Accept-Encoding": "gzip"}, "")
	if got := w.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("non-JSON must not be compressed, got %q", got)
	}
	if w.Body.String() != "<html>hi</html>" {
		t.Fatalf("body mangled: %q", w.Body.String())
	}
}

var gzipHdr = map[string]string{"Accept-Encoding": "gzip"}

func TestGzipSkipsSmallJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Gzip())
	r.GET("/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })
	w := do(r, "GET", "/healthz", "1.2.3.4", gzipHdr, "")
	if w.Code != 200 || w.Header().Get("Content-Encoding") != "" {
		t.Fatalf("small body: code=%d enc=%q", w.Code, w.Header().Get("Content-Encoding"))
	}
	if w.Body.String() != `{"status":"ok"}` {
		t.Fatalf("body %q", w.Body.String())
	}
	if w.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("Vary=%q", w.Header().Get("Vary"))
	}
}

func TestGzipStatusOnlyAndAbort(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Gzip())
	r.GET("/abort", func(c *gin.Context) { c.AbortWithStatus(http.StatusNoContent) })
	r.GET("/status", func(c *gin.Context) { c.Status(http.StatusAccepted) })
	r.GET("/teapot", func(c *gin.Context) { c.AbortWithStatus(http.StatusTeapot) })
	r.GET("/json404", func(c *gin.Context) { c.JSON(404, gin.H{"error": "nope"}) })
	for path, want := range map[string]int{"/abort": 204, "/status": 202, "/teapot": 418, "/json404": 404} {
		w := do(r, "GET", path, "1.2.3.4", gzipHdr, "")
		if w.Code != want {
			t.Errorf("%s: status %d want %d", path, w.Code, want)
		}
		if w.Header().Get("Content-Encoding") != "" {
			t.Errorf("%s: unexpected Content-Encoding", path)
		}
	}
}

func TestGzipPreservesStatusOnCompressedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Gzip())
	r.GET("/big404", func(c *gin.Context) { c.JSON(404, gin.H{"pad": make([]int, 2000)}) })
	w := do(r, "GET", "/big404", "1.2.3.4", gzipHdr, "")
	if w.Code != 404 || w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("code=%d enc=%q", w.Code, w.Header().Get("Content-Encoding"))
	}
}

func TestGzipRedirectAndWriteString(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Gzip())
	r.GET("/old", func(c *gin.Context) { c.Redirect(http.StatusMovedPermanently, "/new") })
	r.GET("/found", func(c *gin.Context) { c.Redirect(http.StatusFound, "/new") })
	r.GET("/text", func(c *gin.Context) { c.String(http.StatusNotFound, "not %s", "here") })
	r.GET("/ws", func(c *gin.Context) {
		c.Status(http.StatusCreated)
		_, _ = c.Writer.WriteString("direct")
	})
	w := do(r, "GET", "/old", "1.2.3.4", gzipHdr, "")
	if w.Code != 301 || w.Header().Get("Location") != "/new" {
		t.Fatalf("redirect: %d %v", w.Code, w.Header())
	}
	if w := do(r, "GET", "/found", "1.2.3.4", gzipHdr, ""); w.Code != 302 {
		t.Fatalf("302 became %d", w.Code)
	}
	w = do(r, "GET", "/text", "1.2.3.4", gzipHdr, "")
	if w.Code != 404 || w.Body.String() != "not here" || w.Header().Get("Content-Encoding") != "" {
		t.Fatalf("c.String: %d %q enc=%q", w.Code, w.Body.String(), w.Header().Get("Content-Encoding"))
	}
	w = do(r, "GET", "/ws", "1.2.3.4", gzipHdr, "")
	if w.Code != 201 || w.Body.String() != "direct" {
		t.Fatalf("WriteString: %d %q", w.Code, w.Body.String())
	}
}

// Status/Written/Size must describe the pending response, not the untouched
// underlying writer.
func TestGzipWriterReportsPendingState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Gzip())
	var sawStatus int
	var wasWritten, afterWritten bool
	var size int
	r.GET("/x", func(c *gin.Context) {
		c.Status(http.StatusAccepted)
		sawStatus, wasWritten = c.Writer.Status(), c.Writer.Written()
		c.JSON(http.StatusAccepted, gin.H{"a": 1})
		afterWritten, size = c.Writer.Written(), c.Writer.Size()
	})
	w := do(r, "GET", "/x", "1.2.3.4", gzipHdr, "")
	if sawStatus != 202 || wasWritten || !afterWritten || size != len(`{"a":1}`) || w.Code != 202 {
		t.Fatalf("status=%d written(before)=%v written(after)=%v size=%d code=%d", sawStatus, wasWritten, afterWritten, size, w.Code)
	}
}

func TestGzipPanicStillAnswers500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Recovery(), Gzip())
	r.GET("/boom", func(c *gin.Context) {
		c.Status(http.StatusOK)
		panic("boom")
	})
	w := do(r, "GET", "/boom", "1.2.3.4", gzipHdr, "")
	if w.Code != 500 || w.Body.Len() == 0 {
		t.Fatalf("panic: code=%d body=%q", w.Code, w.Body.String())
	}
}

func TestGzipFlushStreams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Gzip())
	r.GET("/stream", func(c *gin.Context) {
		c.Header("Content-Type", "application/json")
		_, _ = c.Writer.WriteString(`{"a":`)
		c.Writer.Flush()
		_, _ = c.Writer.WriteString(`1}`)
	})
	w := do(r, "GET", "/stream", "1.2.3.4", gzipHdr, "")
	zr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(zr)
	if string(b) != `{"a":1}` {
		t.Fatalf("stream body %q", b)
	}
}

func TestGzipDoesNotDoubleCompress(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Gzip())
	pre, _ := gzipBytes([]byte(strings.Repeat(`{"k":"v"},`, 500)))
	r.GET("/pre", func(c *gin.Context) {
		c.Header("Content-Type", "application/json")
		c.Header("Content-Encoding", "gzip")
		_, _ = c.Writer.Write(pre)
	})
	w := do(r, "GET", "/pre", "1.2.3.4", gzipHdr, "")
	if !bytes.Equal(w.Body.Bytes(), pre) {
		t.Fatal("already-encoded body was compressed again")
	}
}

func TestAcceptsGzip(t *testing.T) {
	for in, want := range map[string]bool{
		"gzip": true, "gzip, deflate, br": true, "deflate, gzip;q=0.5": true, "GZIP": true,
		"gzip;q=0": false, "identity": false, "": false, "br": false, "gzip; q=0": false,
	} {
		h := http.Header{}
		if in != "" {
			h.Set("Accept-Encoding", in)
		}
		if got := acceptsGzip(h); got != want {
			t.Errorf("%q -> %v want %v", in, got, want)
		}
	}
}
