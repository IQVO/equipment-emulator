package main

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServeReturnsListenError(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	srv := &http.Server{Addr: l.Addr().String(), ReadHeaderTimeout: time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := serve(ctx, srv, time.Hour, func() {}); err == nil {
		t.Fatal("serve must return the listen error")
	}
}

func TestServeShutsDownCleanlyOnCancel(t *testing.T) {
	srv := &http.Server{Addr: "127.0.0.1:0", ReadHeaderTimeout: time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, srv, 5*time.Millisecond, func() {
			select {
			case ticks <- struct{}{}:
			default:
			}
		})
	}()
	<-ticks
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("err = %v", err)
	}
}
