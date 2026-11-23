//go:build !android

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/revamptori/Horizon-manager/internal/core"
	"github.com/revamptori/Horizon-manager/internal/desktop"
	"github.com/revamptori/Horizon-manager/web"
)

func main() {
	if err := run(); err != nil {
		showError(err.Error())
		os.Exit(1)
	}
}
func run() error {
	noBrowser := flag.Bool("no-browser", false, "do not open the local UI automatically")
	dataDir := flag.String("data-dir", "", "application data directory (absolute path)")
	flag.Parse()
	if *dataDir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		*dataDir = filepath.Join(base, "HorizonServerManager")
	}
	engine, err := core.New(core.Options{DataDir: *dataDir})
	if err != nil {
		return err
	}
	unlock, err := lockInstance(*dataDir)
	if err != nil {
		return err
	}
	defer unlock()
	assets := web.Assets()
	if _, err = fs.Stat(assets, "index.html"); err != nil {
		return errors.New("frontend is not embedded; run npm ci && npm run build before building the executable")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	origin := "http://" + listener.Addr().String()
	var secret [32]byte
	if _, err = rand.Read(secret[:]); err != nil {
		return err
	}
	token := hex.EncodeToString(secret[:])
	handler, err := desktop.NewHandler(origin, token, assets, engine)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	engine.OnShutdown = cancel
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 12 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	serverErr := make(chan error, 1)
	go func() { serverErr <- server.Serve(listener) }()
	// Never log the session token or launch URL. The fragment is read once by the UI.
	log.Printf("Horizon %s local host listening at %s (authenticated loopback only)", core.Version, origin)
	if !*noBrowser {
		if err = openBrowser(origin + "/#horizon-token=" + token); err != nil {
			cancel()
			server.Close()
			return fmt.Errorf("could not open the browser: %w", err)
		}
	}
	select {
	case <-ctx.Done():
	case err = <-serverErr:
		if !errors.Is(err, http.ErrServerClosed) {
			cancel()
		}
	}
	shutdown, cancelShutdown := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancelShutdown()
	closeErr := engine.Close(shutdown)
	server.Shutdown(shutdown)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return closeErr
}
