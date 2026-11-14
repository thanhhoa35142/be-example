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

// minGzipBytes is the smallest body worth compressing: below it the gzip
// header/trailer and CPU cost outweigh the saving (and a packet is a packet).
const minGzipBytes = 1024

// gzipPool recycles compressors: a gzip.Writer carries ~1 MB of state, so
// allocating one per request dominates the cost of small responses.
var gzipPool = sync.Pool{New: func() any { return gzip.NewWriter(io.Discard) }}

func getGzip(w io.Writer) *gzip.Writer {
	zw := gzipPool.Get().(*gzip.Writer)
	zw.Reset(w)
	return zw
}

func putGzip(zw *gzip.Writer) {
	zw.Reset(io.Discard) // drop the reference to the response writer
	gzipPool.Put(zw)
}

// gzipBytes compresses b with a pooled writer. ok is false when compression
// would not shrink the body (or failed), in which case callers keep identity.
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

// acceptsGzip reports whether the request's Accept-Encoding allows gzip
// ("gzip;q=0" and absent headers do not).
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

// addVary appends token to Vary unless it is already listed.
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

// Gzip compresses JSON responses for clients that send
// `Accept-Encoding: gzip`. It is registered before the per-route cache
// middleware so the cache stores the identity body (plus its own
// pre-compressed copy, which it sends with Content-Encoding already set so
// this middleware leaves it alone) and each client gets its encoding on the
// way out.
func Gzip() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !acceptsGzip(c.Request.Header) {
			c.Next()
			return
		}
		// Vary must be sent even when this particular response ends up
		// uncompressed, so shared caches key on Accept-Encoding.
		addVary(c.Writer.Header(), "Accept-Encoding")
		w := &gzipResponseWriter{ResponseWriter: c.Writer, size: -1}
		c.Writer = w
		w.run(c)
	}
}

// run executes the rest of the chain. A panic must not flush the held
// bytes (that would send a 200 before Recovery can answer 500), so the
// writer is reset into pass-through mode before the panic continues
// outwards.
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

// gzipResponseWriter delays both the status code and the gzip decision:
// gin calls WriteHeader (via c.Status) before the renderer sets
// Content-Type, so deciding at WriteHeader time would always see an empty
// content type. JSON bodies are held back until minGzipBytes have
// accumulated (or the response ends smaller), then either compressed or sent
// as is. Everything gin's ResponseWriter exposes about the status/body
// (Status, Written, Size, WriteString, WriteHeaderNow) reports the logical
// state of that pending response rather than the untouched underlying writer.
type gzipResponseWriter struct {
	gin.ResponseWriter
	zw       *gzip.Writer
	pending  int    // status set by WriteHeader, not yet forwarded
	held     []byte // JSON bytes awaiting the size decision
	size     int    // identity bytes accepted so far, -1 = none
	decided  bool
	compress bool
	sent     bool // headers forwarded to the underlying writer
	// passthrough: after a panic, forward everything untouched so the
	// recovery handler's response is not stuck in the size-decision buffer.
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

// compressible: JSON that nobody encoded yet.
func (w *gzipResponseWriter) compressible() bool {
	h := w.Header()
	return strings.Contains(h.Get("Content-Type"), "application/json") &&
		h.Get("Content-Encoding") == ""
}

// decide fixes the encoding and forwards the status + headers.
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

// WriteHeaderNow is what gin's AbortWithStatus calls. It must forward the
// pending status, not the underlying writer's default 200.
func (w *gzipResponseWriter) WriteHeaderNow() {
	if w.passthrough {
		w.ResponseWriter.WriteHeaderNow()
		return
	}
	if !w.decided {
		// No body size to wait for: headers go out now.
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

// Flush pushes buffered data downstream; handlers that stream must not
// have their output stuck in the compressor or the size-decision buffer.
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

// finalize settles a response that never reached the decision point (small
// or bodiless) and closes the compressor when it engaged.
func (w *gzipResponseWriter) finalize() {
	if !w.decided {
		w.decide(false) // below minGzipBytes (or no body): identity
	}
	_ = w.flushHeld()
	if w.compress {
		_ = w.zw.Close()
		putGzip(w.zw)
		w.zw = nil
	}
}

var _ gin.ResponseWriter = (*gzipResponseWriter)(nil)
