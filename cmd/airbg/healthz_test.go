package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRunHealthz(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{name: "200 is healthy", status: http.StatusOK, wantErr: false},
		{name: "503 is unhealthy", status: http.StatusServiceUnavailable, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()

			err := runHealthz(strings.TrimPrefix(srv.URL, "http://"), time.Second)
			if (err != nil) != tt.wantErr {
				t.Errorf("runHealthz() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}

	t.Run("unreachable errors within the timeout", func(t *testing.T) {
		start := time.Now()
		err := runHealthz("127.0.0.1:1", 500*time.Millisecond)
		if err == nil {
			t.Fatal("runHealthz() error = nil, want an error for an unreachable address")
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Errorf("runHealthz() took %v, want well under the timeout bound", elapsed)
		}
	})
}
