package httpx

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"airbg.org/internal/metrics"
)

var panicsRecovered = metrics.Counter(
	"airbg_http_panics_recovered_total",
	"Handler panics caught by the recovery middleware.")

// statusRecorder tracks whether a status has been written, so Recover knows
// whether it may still write one.
type statusRecorder struct {
	http.ResponseWriter
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	s.wroteHeader = true
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	// An implicit 200 counts as written; net/http sends one on the first Write.
	s.wroteHeader = true
	return s.ResponseWriter.Write(b)
}

// Recover converts a handler panic into a 500.
//
// The panic value and stack go to the log, never to the client. Panic text
// routinely contains file paths, SQL fragments and internal state — returning it
// hands an attacker a free reconnaissance channel, and this project's whole API
// posture is about not volunteering internals.
//
// http.ErrAbortHandler is re-panicked rather than swallowed: net/http uses it as
// the deliberate "drop this connection silently" signal, and turning it into a
// 500 would both log noise and defeat the abort.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w}

		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler {
				panic(v)
			}

			panicsRecovered.Inc()
			slog.Error("handler panic",
				"panic", v,
				"method", r.Method,
				// The route pattern, not the raw path: logging attacker-supplied
				// paths verbatim invites log injection, and r.Pattern is set by
				// ServeMux from the route table.
				"pattern", r.Pattern,
				"stack", string(debug.Stack()))

			// Only write if nothing has been written yet. Writing a second
			// status produces net/http's "superfluous WriteHeader" and leaves
			// the client with a truncated body under a success status.
			if !rec.wroteHeader {
				rec.ResponseWriter.Header().Set("Content-Type", "application/json; charset=utf-8")
				rec.ResponseWriter.WriteHeader(http.StatusInternalServerError)
				_, _ = rec.ResponseWriter.Write([]byte(`{"error":"internal","message":"Internal server error."}`))
			}
		}()

		next.ServeHTTP(rec, r)
	})
}
