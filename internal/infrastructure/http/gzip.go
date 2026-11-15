package http

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

const minGzipBytes = 1024

var gzipPool = sync.Pool{New: func() any { return gzip.NewWriter(io.Discard) }}

func getGzip(w io.Writer) *gzip.Writer {
	zw := gzipPool.Get().(*gzip.Writer)
	zw.Reset(w)
	return zw
}

func putGzip(zw *gzip.Writer) {
	zw.Reset(io.Discard)
	gzipPool.Put(zw)
}

func gzipBytes(b []byte) (out []byte, ok bool) {
	var buf bytes.Buffer
	buf.Grow(len(b) / 4)
	zw := getGzip(&buf)
	defer putGzip(zw)
	if _, err := zw.Write(b); err != nil {
		return nil, false
	}
	if err := zw.Close(); err != nil {
		return nil, false
	}
	if buf.Len() >= len(b) {
		return nil, false
	}
	return buf.Bytes(), true
}

func acceptsGzip(h http.Header) bool {
	for _, part := range strings.Split(strings.Join(h.Values("Accept-Encoding"), ","), ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(name), "gzip") {
			continue
		}
		if q, ok := strings.CutPrefix(strings.ReplaceAll(strings.ToLower(params), " ", ""), "q="); ok {
			if f, err := strconv.ParseFloat(q, 64); err == nil && f <= 0 {
				return false
			}
		}
		return true
	}
	return false
}

func addVary(h http.Header, token string) {
	for _, v := range h.Values("Vary") {
		for _, t := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(t), token) || strings.TrimSpace(t) == "*" {
				return
			}
		}
	}
	h.Add("Vary", token)
}

func Gzip() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !acceptsGzip(c.Request.Header) {
			c.Next()
			return
		}

		addVary(c.Writer.Header(), "Accept-Encoding")
		w := &gzipResponseWriter{ResponseWriter: c.Writer, size: -1}
		c.Writer = w
		w.run(c)
	}
}

func (w *gzipResponseWriter) run(c *gin.Context) {
	defer func() {
		if r := recover(); r != nil {
			w.abandon()
			panic(r)
		}
	}()
	c.Next()
	w.finalize()
}

type gzipResponseWriter struct {
	gin.ResponseWriter
	zw       *gzip.Writer
	pending  int
	held     []byte
	size     int
	decided  bool
	compress bool
	sent     bool

	passthrough bool
}

func (w *gzipResponseWriter) abandon() {
	if w.decided {
		return
	}
	w.held, w.pending, w.size = nil, 0, -1
	w.passthrough = true
}

func (w *gzipResponseWriter) WriteHeader(code int) {
	if w.passthrough {
		w.ResponseWriter.WriteHeader(code)
		return
	}
	if w.sent {
		return
	}
	if code > 0 {
		w.pending = code
	}
}

func (w *gzipResponseWriter) Status() int {
	if !w.passthrough && !w.sent && w.pending != 0 {
		return w.pending
	}
	return w.ResponseWriter.Status()
}

func (w *gzipResponseWriter) Size() int {
	if w.passthrough {
		return w.ResponseWriter.Size()
	}
	return w.size
}

func (w *gzipResponseWriter) Written() bool {
	if w.passthrough {
		return w.ResponseWriter.Written()
	}
	return w.size != -1
}

func (w *gzipResponseWriter) compressible() bool {
	h := w.Header()
	return strings.Contains(h.Get("Content-Type"), "application/json") &&
		h.Get("Content-Encoding") == ""
}

func (w *gzipResponseWriter) decide(compress bool) {
	if w.decided {
		return
	}
	w.decided = true
	w.compress = compress
	if compress {
		h := w.Header()
		h.Set("Content-Encoding", "gzip")
		h.Del("Content-Length")
		w.zw = getGzip(w.ResponseWriter)
	}
	if w.pending != 0 {
		w.ResponseWriter.WriteHeader(w.pending)
	}
	w.sent = true
}

func (w *gzipResponseWriter) WriteHeaderNow() {
	if w.passthrough {
		w.ResponseWriter.WriteHeaderNow()
		return
	}
	if !w.decided {

		w.decide(w.compressible())
	}
	if w.size == -1 {
		w.size = 0
	}
	w.ResponseWriter.WriteHeaderNow()
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	if w.passthrough {
		return w.ResponseWriter.Write(b)
	}
	if w.size == -1 {
		w.size = 0
	}
	w.size += len(b)
	if !w.decided {
		if !w.compressible() {
			w.decide(false)
		} else {
			w.held = append(w.held, b...)
			if len(w.held) < minGzipBytes {
				return len(b), nil
			}
			w.decide(true)
			return len(b), w.flushHeld()
		}
	}
	if w.compress {
		return w.zw.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

func (w *gzipResponseWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *gzipResponseWriter) flushHeld() error {
	if len(w.held) == 0 {
		return nil
	}
	held := w.held
	w.held = nil
	var err error
	if w.compress {
		_, err = w.zw.Write(held)
	} else {
		_, err = w.ResponseWriter.Write(held)
	}
	return err
}

func (w *gzipResponseWriter) Flush() {
	if w.passthrough {
		w.ResponseWriter.Flush()
		return
	}
	if !w.decided {
		w.decide(w.compressible())
	}
	_ = w.flushHeld()
	if w.compress {
		_ = w.zw.Flush()
	}
	w.ResponseWriter.Flush()
}

func (w *gzipResponseWriter) finalize() {
	if !w.decided {
		w.decide(false)
	}
	_ = w.flushHeld()
	if w.compress {
		_ = w.zw.Close()
		putGzip(w.zw)
		w.zw = nil
	}
}

var _ gin.ResponseWriter = (*gzipResponseWriter)(nil)
