package rest

import (
	"context"
	"net/http"
	"time"
)

// serveGracefully starts srv and blocks until it stops or ctx is cancelled.
// On cancellation it drains in-flight requests up to shutdownTimeout.
func serveGracefully(ctx context.Context, srv *http.Server, shutdownTimeout time.Duration) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
