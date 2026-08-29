package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"testing"

	"github.com/thorstenkramm/mia/internal/logging"
)

func TestListenSocketRejectsActiveUnixSocketAndSetsPermissions(t *testing.T) {
	file, err := os.CreateTemp("/tmp", "mia-sock-")
	if err != nil {
		t.Fatal(err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	active, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := active.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Error(err)
		}
	}()
	if _, err := listenSocket("unix:"+path, ""); err == nil {
		t.Fatal("listenSocket accepted an active socket")
	}
	if err := active.Close(); err != nil {
		t.Fatal(err)
	}
	listener, err := listenSocket("unix:"+path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	}()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o660 {
		t.Fatalf("Unix socket permissions = %04o", info.Mode().Perm())
	}
}

func TestServeGracefulShutdownDoesNotReturnClosedListenerError(t *testing.T) {
	logger, err := logging.New("", "error", "json")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := logger.Close(); err != nil {
			t.Error(err)
		}
	}()
	context, cancel := context.WithCancel(context.Background())
	cancel()
	if err := serve(context, "127.0.0.1:0", "", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), logger); err != nil {
		t.Fatalf("serve shutdown error = %v", err)
	}
}
