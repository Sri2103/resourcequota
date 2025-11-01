package health

import (
	"net/http"
	"sync/atomic"
)

var isReady atomic.Bool

func init() {
	isReady.Store(false)
}

func SetReady() {
	isReady.Store(true)
}

func HealthzHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func ReadyzHandler(w http.ResponseWriter, r *http.Request) {
	if !isReady.Load() {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ready"))
}
