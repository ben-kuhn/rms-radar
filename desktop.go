//go:build desktop

package main

import (
	"context"
	"flag"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/ben-kuhn/rms-radar/internal/app"
	webview "github.com/webview/webview_go"
)

// Native-window build. Requires CGo and the platform webview runtime:
// WebKitGTK 4.1 (Linux), WebView2 (Windows), WKWebView (macOS). The window
// hosts the same local server and UI as the web binary; closing it exits.
func main() {
	cf := registerCommonFlags(
		"127.0.0.1:0",
		defaultDesktopCacheDir(),
		"local listen address (default: an ephemeral localhost port)",
		"cache directory for gateway list, tiles, and logs",
		true,
	)
	debug := flag.Bool("debug", envBool("RMSRADAR_DEBUG", false), "enable the webview developer tools")
	flag.Parse()
	addr, serviceCodes, refresh, radioPostRefresh, cacheDir := cf.Listen, cf.ServiceCodes, cf.Refresh, cf.RadioPostRefresh, cf.CacheDir
	accessKey, historyHours := cf.AccessKey, cf.HistoryHours
	voacapURL, voacapExec, voacapData := cf.VOACAPURL, cf.VOACAPExecutable, cf.VOACAPDataDir
	voacapDlURL, voacapDlDir, voacapDlSum := cf.VOACAPDownloadURL, cf.VOACAPDownloadDir, cf.VOACAPDownloadSHA256
	adminToken, voacapAuto := cf.AdminToken, cf.VOACAPAuto
	webDir, tileURL, tileCacheMax, vhfRange := cf.WebDir, cf.TileURL, cf.TileCacheMax, cf.VHFRange

	// Native builds have no console on Windows, so also write logs to the cache.
	if err := os.MkdirAll(*cacheDir, 0o755); err == nil {
		logPath := filepath.Join(*cacheDir, "rms-radar.log")
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			log.SetOutput(teeWriter{os.Stderr, f})
			defer f.Close()
		}
	}

	// Optional: bootstrap a VOACAP engine (macOS/Windows have no package manager).
	*voacapExec, *voacapData = initVOACAP(*voacapExec, *voacapData, *voacapDlURL, *voacapDlDir, *voacapDlSum, *voacapURL, version, *voacapAuto)

	codes := splitCSV(*serviceCodes)
	log.Printf("RMS Radar desktop %s | service codes: %v", version, codes)

	webFSys, err := webFilesystem(*webDir)
	if err != nil {
		log.Fatalf("web: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, err := app.Start(ctx, app.Config{
		Version:           version,
		Listen:            *addr,
		ServiceCodes:      codes,
		HistoryHours:      *historyHours,
		CacheDir:          *cacheDir,
		AccessKey:         *accessKey,
		Refresh:           *refresh,
		RadioPostRefresh:  *radioPostRefresh,
		VOACAPURL:         *voacapURL,
		VOACAPExecutable:  *voacapExec,
		VOACAPDataDir:     *voacapData,
		TileURL:           *tileURL,
		TileCacheMaxBytes: int64(*tileCacheMax) << 20,
		VHFMaxRangeKm:     *vhfRange,
		WebFS:             webFSys,
		DesktopMode:       true,
		AdminToken:        *adminToken,
	})
	if err != nil {
		log.Fatalf("start: %v", err)
	}

	w := webview.New(*debug)
	if w == nil {
		a.Close()
		log.Fatal("webview: failed to create a window (is WebView2/WebKitGTK installed?)")
	}
	defer w.Destroy()
	w.SetTitle("RMS Radar — Winlink and RadioPost Gateway Map")
	w.SetSize(1200, 820, webview.HintNone)
	w.Navigate(a.URL())
	log.Printf("window: %s", a.URL())
	w.Run()
	a.Close()
}

func defaultDesktopCacheDir() string {
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "rms-radar")
	}
	return "data"
}

// teeWriter keeps logging to the console when there is one, but never lets a
// failing stderr (Windows GUI builds have none) drop the file log.
type teeWriter struct{ a, b io.Writer }

func (t teeWriter) Write(p []byte) (int, error) {
	_, _ = t.a.Write(p)
	return t.b.Write(p)
}
