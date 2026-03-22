package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/leorafaelmb/JellyTorrent/internal/dht"
)

func main() {
	port := flag.Int("port", 6881, "UDP port for DHT")
	logPath := flag.String("log", "", "path to JSON log file (default: stderr)")
	statePath := flag.String("state", "dht_state.dat", "path to routing table persistence file")
	infohashFlag := flag.String("infohash", "", "comma-separated hex info hashes to announce")
	rateLimit := flag.Int("ratelimit", 50, "max DHT queries per minute per IP (0 to disable)")
	metricsPort := flag.Int("metrics-port", 0, "HTTP port for /metrics endpoint (0 to disable)")
	bep42 := flag.String("bep42", "off", "BEP 42 node ID restriction mode: off, log, enforce")
	debug := flag.Bool("debug", false, "enable debug logging")
	flag.Parse()

	logger := setupLogger(*logPath, *debug)

	// Ensure state directory exists.
	if dir := filepath.Dir(*statePath); dir != "." {
		os.MkdirAll(dir, 0755)
	}

	// Parse info hashes before starting DHT.
	var infoHashes [][20]byte
	if *infohashFlag != "" {
		var err error
		infoHashes, err = parseInfoHashes(*infohashFlag)
		if err != nil {
			logger.Error("failed to parse info hashes", "error", err)
			os.Exit(1)
		}
	}

	// Set up signal handling early so a signal during bootstrap is caught.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	bep42Mode := dht.BEP42Off
	switch *bep42 {
	case "log":
		bep42Mode = dht.BEP42Log
	case "enforce":
		bep42Mode = dht.BEP42Enforce
	}

	d, err := dht.New(
		dht.WithPort(*port),
		dht.WithLogger(logger),
		dht.WithRoutingTable(*statePath),
		dht.WithRateLimit(*rateLimit, 1*time.Minute),
		dht.WithMetricsPort(*metricsPort),
		dht.WithBEP42(bep42Mode),
	)
	if err != nil {
		logger.Error("failed to create DHT", "error", err)
		os.Exit(1)
	}

	logger.Info("DHT started", "port", *port)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := d.Bootstrap(ctx); err != nil {
		logger.Warn("bootstrap failed", "error", err)
	} else {
		logger.Info("bootstrap complete")
	}
	cancel()

	// Initial announce for all info hashes.
	for _, ih := range infoHashes {
		announceCtx, announceCancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := d.Announce(announceCtx, ih, *port); err != nil {
			logger.Warn("announce failed", "infohash", hex.EncodeToString(ih[:]), "error", err)
		} else {
			logger.Info("announced", "infohash", hex.EncodeToString(ih[:]))
		}
		announceCancel()
	}

	// Periodic re-announce goroutine.
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(15 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				for _, ih := range infoHashes {
					reCtx, reCancel := context.WithTimeout(context.Background(), 30*time.Second)
					d.Announce(reCtx, ih, *port)
					reCancel()
				}
			case <-done:
				return
			}
		}
	}()

	logger.Info("running, press Ctrl+C to stop")

	// Wait for first signal.
	<-sigCh
	logger.Info("shutting down...")

	// Second signal force-exits.
	go func() {
		<-sigCh
		logger.Warn("forced exit")
		os.Exit(1)
	}()

	close(done)

	if err := d.Save(*statePath); err != nil {
		logger.Error("failed to save routing table", "error", err)
	} else {
		logger.Info("routing table saved", "path", *statePath)
	}

	if err := d.Close(); err != nil {
		logger.Error("failed to close DHT", "error", err)
	}

	logger.Info("shutdown complete")
}

func setupLogger(logPath string, debug bool) *slog.Logger {
	opts := &slog.HandlerOptions{}
	if debug {
		opts.Level = slog.LevelDebug
	}

	if logPath == "" {
		return slog.New(slog.NewJSONHandler(os.Stderr, opts))
	}

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open log file: %v\n", err)
		os.Exit(1)
	}
	return slog.New(slog.NewJSONHandler(f, opts))
}

func parseInfoHashes(s string) ([][20]byte, error) {
	parts := strings.Split(s, ",")
	hashes := make([][20]byte, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		b, err := hex.DecodeString(p)
		if err != nil {
			return nil, fmt.Errorf("invalid hex %q: %w", p, err)
		}
		if len(b) != 20 {
			return nil, fmt.Errorf("info hash %q must be 20 bytes, got %d", p, len(b))
		}
		var ih [20]byte
		copy(ih[:], b)
		hashes = append(hashes, ih)
	}
	return hashes, nil
}
