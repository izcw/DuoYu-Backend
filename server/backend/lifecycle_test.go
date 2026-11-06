package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServeHTTPDrainsAcceptedRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		io.WriteString(w, "committed")
	})}
	t.Cleanup(func() { server.Close(); listener.Close() })
	done := make(chan error, 1)
	go func() { done <- serveHTTP(ctx, server, listener, time.Second) }()
	response := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 2 * time.Second}
		res, err := client.Get("http://" + listener.Addr().String())
		if err == nil {
			defer res.Body.Close()
			body, readErr := io.ReadAll(res.Body)
			if readErr != nil {
				err = readErr
			} else if string(body) != "committed" {
				err = errors.New("incomplete response")
			}
		}
		response <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("request never arrived")
	}
	cancel()
	returned := false
	select {
	case err := <-done:
		returned = true
		t.Errorf("service returned before accepted request completed: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	if err := <-response; err != nil {
		t.Error(err)
	}
	if !returned {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
}

func TestServeHTTPDeadlineClosesConnections(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered, disconnected := make(chan struct{}), make(chan struct{})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(disconnected)
	})}
	t.Cleanup(func() { server.Close(); listener.Close() })
	done := make(chan error, 1)
	go func() { done <- serveHTTP(ctx, server, listener, 30*time.Millisecond) }()
	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		client := &http.Client{Timeout: 2 * time.Second}
		if response, err := client.Get("http://" + listener.Addr().String()); err == nil {
			response.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("request never arrived")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("want deadline error, got %v", err)
	}
	select {
	case <-disconnected:
	case <-time.After(200 * time.Millisecond):
		t.Error("active connection not closed")
	}
	server.Close()
	<-clientDone
}

func TestServeHTTPReportsListenerFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = serveHTTP(ctx, &http.Server{}, listener, time.Second)
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("listener error lost: %v", err)
	}
}
