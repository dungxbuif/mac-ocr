package rest

import (
	"net/http"
	"testing"
	"time"
)

func TestProductionHTTPServerHasResourceTimeouts(t *testing.T) {
	srv := newHTTPServer(":0", http.NotFoundHandler())
	if srv.ReadHeaderTimeout != 5*time.Second {
		t.Fatalf("unexpected ReadHeaderTimeout: %s", srv.ReadHeaderTimeout)
	}
	if srv.ReadTimeout != 2*time.Minute {
		t.Fatalf("unexpected ReadTimeout: %s", srv.ReadTimeout)
	}
	if srv.IdleTimeout != 2*time.Minute {
		t.Fatalf("unexpected IdleTimeout: %s", srv.IdleTimeout)
	}
	if srv.MaxHeaderBytes != 64<<10 {
		t.Fatalf("unexpected MaxHeaderBytes: %d", srv.MaxHeaderBytes)
	}
	if srv.WriteTimeout != 0 {
		t.Fatalf("WriteTimeout must remain disabled for SSE: %s", srv.WriteTimeout)
	}
}
