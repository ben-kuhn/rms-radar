// Package app wires the loader, propagation conditions, HTTP server, and
// background refresh loop together. Both the web/service binary and the
// native-window desktop binary start from here.
package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ben-kuhn/rms-radar/internal/atomicfile"
	"github.com/ben-kuhn/rms-radar/internal/cmsapi"
	"github.com/ben-kuhn/rms-radar/internal/propagation"
	"github.com/ben-kuhn/rms-radar/internal/radiopost"
	"github.com/ben-kuhn/rms-radar/internal/server"
)

// Config is the shared startup configuration.
type Config struct {
	Version           string
	Listen            string
	ServiceCodes      []string
	HistoryHours      int
	CacheDir          string
	AccessKey         string
	Refresh           time.Duration
	RadioPostRefresh  time.Duration
	VOACAPURL         string
	VOACAPExecutable  string
	VOACAPDataDir     string
	TileURL           string
	TileCacheMaxBytes int64
	VHFMaxRangeKm     float64
	WebFS             fs.FS

	// DesktopToken enables the browser desktop lifecycle (web binary only). The
	// native-window binary leaves it empty and quits when its window closes.
	DesktopToken string

	// DesktopMode enables same-origin service-code editing without an admin
	// token. Server/daemon builds leave it false unless AdminToken is set.
	DesktopMode bool
	// AdminToken protects service-code changes with the X-RMS-Radar-Admin
	// header. It also enables the endpoint in daemon mode.
	AdminToken string
}

// App is a running instance.
type App struct {
	loader    *cmsapi.Loader
	radiopost *radiopost.Loader
	heur      *propagation.Heuristic
	srv       *server.Server
	http      *http.Server
	ln        net.Listener
	cancel    context.CancelFunc
	done      chan struct{}
}

// Start builds the catalog, seeds it from cache/embedded data, starts the
// refresh loop and the HTTP listener, and returns once the listener is up.
func Start(parent context.Context, cfg Config) (*App, error) {
	if cfg.Refresh <= 0 {
		return nil, fmt.Errorf("refresh interval must be positive (got %s)", cfg.Refresh)
	}
	seedCodes, err := cmsapi.NormalizeServiceCodes(cfg.ServiceCodes)
	if err != nil {
		return nil, fmt.Errorf("service codes: %w", err)
	}
	if len(seedCodes) == 0 {
		seedCodes = append([]string(nil), cmsapi.DefaultServiceCodes...)
	}
	ctx, cancel := context.WithCancel(parent)
	a := &App{cancel: cancel, done: make(chan struct{})}

	tileCache := ""
	serviceCodesPath := ""
	if cfg.CacheDir != "" {
		tileCache = filepath.Join(cfg.CacheDir, "tiles")
		serviceCodesPath = filepath.Join(cfg.CacheDir, "service_codes.json")
	}
	codes := serviceCodesFromDisk(serviceCodesPath, seedCodes)
	a.loader = cmsapi.NewLoader(cfg.CacheDir, codes, cfg.HistoryHours, cfg.AccessKey)
	a.radiopost = radiopost.NewLoader(cfg.RadioPostRefresh)
	a.heur = propagation.NewHeuristic(cfg.CacheDir)
	a.srv = server.New(server.Config{
		Version:           cfg.Version,
		VOACAPURL:         cfg.VOACAPURL,
		VOACAPExecutable:  cfg.VOACAPExecutable,
		VOACAPDataDir:     cfg.VOACAPDataDir,
		TileURL:           cfg.TileURL,
		TileCacheDir:      tileCache,
		TileCacheMaxBytes: cfg.TileCacheMaxBytes,
		VHFMaxRangeKm:     cfg.VHFMaxRangeKm,
		RadioPost:         a.radiopost,
		ServiceCodesPath:  serviceCodesPath,
		AdminToken:        cfg.AdminToken,
		DesktopMode:       cfg.DesktopMode,
		DesktopToken:      cfg.DesktopToken,
		OnQuit:            cancel,
	}, cfg.WebFS, a.loader, a.heur)

	// Seed immediately from cache or the embedded snapshot (never blocks on the
	// network), then refresh from the network in the background.
	a.loader.Seed()
	if err := a.srv.Refresh(ctx); err != nil {
		log.Printf("warning: parse gateways: %v", err)
	}
	go a.radiopost.Run(ctx)

	go func() {
		a.heur.RefreshConditions(ctx)
		if err := a.loader.Refresh(ctx, true); err == nil {
			_ = a.srv.Refresh(ctx)
		}
		ticker := time.NewTicker(cfg.Refresh)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				a.heur.RefreshConditions(ctx)
				if err := a.loader.Refresh(ctx, true); err != nil {
					log.Printf("refresh: %v", err)
				} else if err := a.srv.Refresh(ctx); err != nil {
					log.Printf("parse: %v", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		cancel()
		return nil, err
	}
	a.ln = ln
	a.http = &http.Server{
		Handler:           a.srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// Strictly greater than any rank deadline, so a long rank's response
		// is never aborted mid-write.
		WriteTimeout: server.RankTimeoutVOACAP + 2*time.Minute,
		IdleTimeout:  120 * time.Second,
	}
	go func() {
		if err := a.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("http server: %v", err)
			cancel()
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = a.http.Shutdown(shutdownCtx)
		close(a.done)
	}()
	return a, nil
}

func sameServiceCodes(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func isPreviousServiceDefault(codes []string) bool {
	// Sorted forms of every default set used before 0.2.9. The three-code
	// MERCURY/PUBLIC entry was observed on an older CarKit desktop install and
	// is included so that one-time migration can reach it.
	previous := [][]string{
		{"EMCOMM", "PUBLIC"},
		{"MERCURY", "PUBLIC"},
		{"MERCURY", "PUBLIC", "VARDEFM"},
		{"EMCOMM", "MERCURY", "PUBLIC"},
		{"EMCOMM", "MERCURY", "PUBLIC", "VARDEFM"},
		{"DATA2G", "EMCOMM", "MERCURY", "MODEM73", "PUBLIC", "VARDEFM"},
		{"DATA2G", "IONHF", "MERCURY", "MODEM73", "PUBLIC", "VARDEFM"},
		{"DATA2G", "EMCOMM", "IONHF", "MERCURY", "MODEM73", "PUBLIC", "VARDEFM"},
	}
	for _, p := range previous {
		if sameServiceCodes(codes, p) {
			return true
		}
	}
	return false
}

func serviceCodesSeedHash(codes []string) string {
	sum := sha256.Sum256([]byte(strings.Join(codes, "\x00")))
	return hex.EncodeToString(sum[:])
}

func writeServiceCodeMarker(path, hash string) {
	_ = atomicfile.Write(path, []byte(hash+"\n"), 0o644)
}

func writeLegacyServiceMarker(path string) {
	_ = atomicfile.Write(path, []byte("v1\n"), 0o644)
}

// serviceCodesFromDisk uses a persisted UI selection while the configured seed
// is unchanged, and re-seeds when the operator changes the configured list. The
// seed hash marker lets an explicit UI choice survive restarts without making
// operator configuration changes ineffective.
func serviceCodesFromDisk(path string, fallback []string) []string {
	if path == "" {
		return fallback
	}
	seedHash := serviceCodesSeedHash(fallback)
	markerPath := path + ".seed"
	legacyPath := path + ".legacy-v1"
	marker, _ := os.ReadFile(markerPath)
	markerCurrent := strings.TrimSpace(string(marker)) == seedHash
	_, legacyErr := os.Stat(legacyPath)
	legacyDone := legacyErr == nil

	b, err := os.ReadFile(path)
	if err != nil {
		writeServiceCodeMarker(markerPath, seedHash)
		writeLegacyServiceMarker(legacyPath)
		return fallback
	}
	var codes []string
	if json.Unmarshal(b, &codes) != nil {
		writeServiceCodeMarker(markerPath, seedHash)
		writeLegacyServiceMarker(legacyPath)
		return fallback
	}
	normalized, nerr := cmsapi.NormalizeServiceCodes(codes)
	if nerr != nil || len(normalized) == 0 {
		writeServiceCodeMarker(markerPath, seedHash)
		writeLegacyServiceMarker(legacyPath)
		return fallback
	}
	// One-time migration for any historical default, independent of the seed
	// marker. A custom selection is preserved and marked done.
	if !legacyDone {
		if isPreviousServiceDefault(normalized) {
			if b, err := json.Marshal(fallback); err == nil {
				_ = atomicfile.Write(path, b, 0o644)
			}
			writeServiceCodeMarker(markerPath, seedHash)
			writeLegacyServiceMarker(legacyPath)
			return fallback
		}
		writeLegacyServiceMarker(legacyPath)
	}
	if markerCurrent {
		return normalized
	}
	// A marker from a different seed means the operator changed the configured
	// list, which is authoritative. No marker means this is a pre-marker custom
	// selection, which is preserved.
	if len(marker) == 0 {
		writeServiceCodeMarker(markerPath, seedHash)
		return normalized
	}
	if b, err := json.Marshal(fallback); err == nil {
		_ = atomicfile.Write(path, b, 0o644)
	}
	writeServiceCodeMarker(markerPath, seedHash)
	return fallback
}

// URL returns a browser-friendly base URL for the listener. A :0 listen
// address reports its actual port; wildcard binds are shown as 127.0.0.1.
func (a *App) URL() string {
	host, port, err := net.SplitHostPort(a.ln.Addr().String())
	if err != nil {
		return "http://" + a.ln.Addr().String()
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// Wait blocks until the HTTP server has shut down.
func (a *App) Wait() { <-a.done }

// Close stops the app and waits for the HTTP server to shut down.
func (a *App) Close() {
	a.cancel()
	<-a.done
}
