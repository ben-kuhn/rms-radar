//go:build !desktop

package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ben-kuhn/rms-radar/internal/app"
	"github.com/ben-kuhn/rms-radar/internal/browser"
)

// Web/service binary. The native-window build lives in desktop.go behind the
// "desktop" build tag, so the default build stays CGO-free.
func main() {
	cf := registerCommonFlags(
		"127.0.0.1:8079",
		"./data",
		"HTTP listen address (use 0.0.0.0:8079 to serve the LAN)",
		"cache directory for gateway list and propagation data",
		false,
	)
	daemonShort := flag.Bool("d", envBool("RMSRADAR_DAEMON", false), "run as a background service (no browser, no auto-exit)")
	daemonLong := flag.Bool("daemon", envBool("RMSRADAR_DAEMON", false), "run as a background service (alias for -d)")
	openBrowser := flag.Bool("open-browser", envBool("RMSRADAR_OPEN_BROWSER", true), "open the web UI in the default browser on start")
	flag.Parse()
	addr, serviceCodes, refresh, radioPostRefresh, cacheDir := cf.Listen, cf.ServiceCodes, cf.Refresh, cf.RadioPostRefresh, cf.CacheDir
	accessKey, historyHours := cf.AccessKey, cf.HistoryHours
	voacapURL, voacapExec, voacapData := cf.VOACAPURL, cf.VOACAPExecutable, cf.VOACAPDataDir
	voacapDlURL, voacapDlDir, voacapDlSum := cf.VOACAPDownloadURL, cf.VOACAPDownloadDir, cf.VOACAPDownloadSHA256
	adminToken, voacapAuto := cf.AdminToken, cf.VOACAPAuto
	webDir, tileURL, tileCacheMax, vhfRange := cf.WebDir, cf.TileURL, cf.TileCacheMax, cf.VHFRange
	isDaemon := *daemonShort || *daemonLong
	if isDaemon {
		*openBrowser = false
	}

	// Resolve VOACAP: explicit download, local install, then the release bundle.
	*voacapExec, *voacapData = initVOACAP(*voacapExec, *voacapData, *voacapDlURL, *voacapDlDir, *voacapDlSum, *voacapURL, version, *voacapAuto)

	codes := splitCSV(*serviceCodes)
	mode := "desktop"
	if isDaemon {
		mode = "daemon"
	}
	log.Printf("RMS Radar web %s | %s mode | service codes: %v", version, mode, codes)

	webFSys, err := webFilesystem(*webDir)
	if err != nil {
		log.Fatalf("web: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	desktopToken := ""
	if !isDaemon {
		desktopToken = randomHex(16)
	}

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
		DesktopToken:      desktopToken,
		DesktopMode:       !isDaemon,
		AdminToken:        *adminToken,
	})
	if err != nil {
		log.Fatal(err)
	}

	if *openBrowser {
		pageURL := a.URL()
		if desktopToken != "" {
			pageURL += "/?desktop=" + desktopToken
		}
		go func() {
			time.Sleep(400 * time.Millisecond)
			if err := browser.Open(pageURL); err != nil {
				log.Printf("open browser: %v", err)
			}
		}()
	}

	log.Printf("listening on %s", a.URL())
	a.Wait()
}
