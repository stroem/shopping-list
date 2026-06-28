package web

import (
	"bytes"
	"context"
	"net/http"
	"sync"
	"time"
)

// DefaultRequestTimeout is the fallback bound applied when a caller wires the
// timeout middleware with a non-positive duration (e.g. a zero-value Deps).
const DefaultRequestTimeout = 5 * time.Second

// Timeout returns middleware that bounds every request to d. It sets a deadline
// on the request context — so downstream Postgres queries cancel via pgx and
// release their pooled connection promptly — and, if the handler has not
// finished when d elapses, writes a single JSON 504 ({"error":...}) response.
//
// The handler runs against an in-memory buffering writer guarded by a mutex.
// Whichever of "handler done" or "deadline fired" wins flushes the real
// ResponseWriter exactly once; a late handler that wakes after the timeout finds
// the writer closed and its Write/WriteHeader become no-ops, so the body is
// never written twice. This mirrors net/http's TimeoutHandler but emits our JSON
// error shape and a 504 instead of a plain-text 503.
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	if d <= 0 {
		d = DefaultRequestTimeout
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()

			tw := &timeoutWriter{header: make(http.Header)}
			done := make(chan struct{})
			panicChan := make(chan any, 1)
			go func() {
				defer func() {
					if p := recover(); p != nil {
						panicChan <- p
					}
				}()
				next.ServeHTTP(tw, r.WithContext(ctx))
				close(done)
			}()

			select {
			case p := <-panicChan:
				// Re-panic on the serving goroutine so the outer Recoverer
				// middleware turns it into a JSON 500.
				panic(p)
			case <-done:
				tw.commit(w)
			case <-ctx.Done():
				tw.mu.Lock()
				tw.timedOut = true
				tw.mu.Unlock()
				Error(w, http.StatusGatewayTimeout, "request timed out")
			}
		})
	}
}

// timeoutWriter buffers a handler's response so the timeout middleware can
// decide, under a lock, whether to flush it or replace it with a 504.
type timeoutWriter struct {
	mu          sync.Mutex
	header      http.Header
	buf         bytes.Buffer
	code        int
	wroteHeader bool
	timedOut    bool
}

func (tw *timeoutWriter) Header() http.Header { return tw.header }

func (tw *timeoutWriter) WriteHeader(code int) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.timedOut || tw.wroteHeader {
		return
	}
	tw.wroteHeader = true
	tw.code = code
}

func (tw *timeoutWriter) Write(b []byte) (int, error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.timedOut {
		// The deadline already won and the 504 is being written; drop the late
		// handler's bytes so the response is never written twice.
		return 0, http.ErrHandlerTimeout
	}
	if !tw.wroteHeader {
		tw.wroteHeader = true
		tw.code = http.StatusOK
	}
	return tw.buf.Write(b)
}

// commit flushes the buffered response to the real writer. Called only on the
// "handler done" branch, after which the handler goroutine has returned.
func (tw *timeoutWriter) commit(w http.ResponseWriter) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	dst := w.Header()
	for k, vv := range tw.header {
		dst[k] = vv
	}
	if tw.code == 0 {
		tw.code = http.StatusOK
	}
	w.WriteHeader(tw.code)
	_, _ = w.Write(tw.buf.Bytes())
}
