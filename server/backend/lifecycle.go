package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

func serveHTTP(ctx context.Context, server *http.Server, listener net.Listener, grace time.Duration) error {
	serving := make(chan error, 1)
	go func() { serving <- server.Serve(listener) }()
	select {
	case err := <-serving:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), grace)
		defer cancel()
		err := server.Shutdown(shutdown)
		if err != nil {
			// Shutdown leaves active connections open when its context expires.
			// Close them explicitly before returning control to resource cleanup.
			server.Close()
		}
		serveErr := <-serving
		if err != nil {
			return err
		}
		if errors.Is(serveErr, http.ErrServerClosed) {
			return nil
		}
		return serveErr
	}
}
