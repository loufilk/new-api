package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	socket := flag.String("socket", "/run/new-api/fingerprint.sock", "absolute path to the private Unix socket")
	dataDir := flag.String("data-dir", "./data", "local directory containing pinned unified_bank.json and shared_detector.bin")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *socket, *dataDir); err != nil {
		// Fixed errors only. Never print request bodies, upstream errors or credentials.
		log.Print(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, socket, dataDir string) error {
	engine, err := LoadEngine(ctx, dataDir)
	if err != nil {
		return err
	}
	listener, cleanup, err := listenUnix(socket)
	if err != nil {
		return err
	}
	defer cleanup()
	server := newServer(engine)
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	select {
	case err := <-finished:
		if !errors.Is(err, http.ErrServerClosed) {
			return errors.New("fingerprint service stopped unexpectedly")
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if server.Shutdown(shutdown) != nil {
			_ = server.Close()
		}
		<-finished
	}
	return nil
}

// A private directory prevents access before chmod and prevents other users
// replacing the socket. Existing files (including stale sockets) are never removed.
func listenUnix(path string) (*net.UnixListener, func(), error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, nil, errors.New("fingerprint socket must be an absolute clean path")
	}
	dir, err := os.Lstat(filepath.Dir(path))
	if err != nil || !dir.IsDir() || dir.Mode().Perm()&0o077 != 0 {
		return nil, nil, errors.New("fingerprint socket requires an existing private directory")
	}
	stat, ok := dir.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return nil, nil, errors.New("fingerprint socket directory must be owned by the service user")
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return nil, nil, errors.New("fingerprint socket path already exists or is unavailable")
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, nil, errors.New("fingerprint socket unavailable")
	}
	listener.SetUnlinkOnClose(false)
	info, err := os.Lstat(path)
	if err != nil {
		_ = listener.Close()
		return nil, nil, errors.New("fingerprint socket unavailable")
	}
	cleanup := func() {
		_ = listener.Close()
		// Remove only the socket this process created, not a replacement path.
		if current, err := os.Lstat(path); err == nil && os.SameFile(info, current) {
			_ = os.Remove(path)
		}
	}
	if os.Chmod(path, 0o600) != nil {
		cleanup()
		return nil, nil, errors.New("fingerprint socket permissions unavailable")
	}
	return listener, cleanup, nil
}
