package httpapi

import "net/http"

// Healthz always reports OK once the process is serving HTTP at all.
func Healthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// ReadyProbe reports readiness based on a caller-supplied check (e.g.
// "can we reach Redis"), so cmd/server can wire in real dependency checks
// without this package needing to know about them.
type ReadyProbe func() error

func Readyz(probe ReadyProbe) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if probe != nil {
			if err := probe(); err != nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(err.Error()))
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	}
}
