package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ben-kuhn/rms-radar/internal/cmsapi"
	"github.com/ben-kuhn/rms-radar/internal/propagation"
	"github.com/ben-kuhn/rms-radar/internal/voacapget"
)

// version is overridable at link time via -ldflags "-X main.version=...".
var version = "1.0.16"

//go:embed all:web
var webFS embed.FS

func webFilesystem(dir string) (fs.FS, error) {
	if dir != "" {
		return os.DirFS(dir), nil
	}
	return fs.Sub(webFS, "web")
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func defaultVOACAPDownloadDir() string {
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "rms-radar", "voacap")
	}
	return filepath.Join(".", "data", "voacap")
}

// initVOACAP applies the shared startup order: an explicit download, then a
// local installation, then the automatic release bundle. It returns empty
// paths when no engine is available.
func initVOACAP(executable, dataDir, downloadURL, downloadDir, downloadSHA, remoteURL, version string, auto bool) (string, string) {
	if downloadURL != "" && executable == "" {
		if err := validateVOACAPDownloadURL(downloadURL, downloadSHA); err != nil {
			log.Printf("voacapl download refused: %v", err)
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			exe, data, err := voacapget.Fetch(ctx, downloadURL, downloadDir, downloadSHA)
			cancel()
			if err != nil {
				log.Printf("voacapl download failed: %v", err)
			} else {
				executable, dataDir = exe, data
				log.Printf("voacapl ready: %s (data %s)", exe, data)
			}
		}
	}
	if executable == "" {
		if exe, data := propagation.Detect(); exe != "" {
			executable = exe
			if dataDir == "" {
				dataDir = data
			}
			log.Printf("voacapl: detected %s (data %s)", exe, data)
		}
	}
	if executable == "" && downloadURL == "" && remoteURL == "" {
		if exe, data := maybeAutoDownloadVOACAP(auto, downloadDir, version); exe != "" {
			executable, dataDir = exe, data
		}
	}
	return executable, dataDir
}

func validateVOACAPDownloadURL(raw, sha string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return nil
	case "http":
		if strings.TrimSpace(sha) != "" {
			return nil
		}
		return errors.New("http VOACAP download requires -voacap-download-sha256")
	default:
		return fmt.Errorf("unsupported VOACAP download scheme %q", u.Scheme)
	}
}

// maybeAutoDownloadVOACAP fetches the signed release bundle for this platform
// when no engine was configured. Failures never block startup: they are logged
// and the heuristic predictor stays in use.
func maybeAutoDownloadVOACAP(enabled bool, destDir, version string) (string, string) {
	if !enabled {
		return "", ""
	}
	return downloadVOACAP(voacapget.BundleURL(version), destDir)
}

func downloadVOACAP(url, destDir string) (string, string) {
	return downloadVOACAPOpts(url, destDir, true)
}

func downloadVOACAPOpts(url, destDir string, requireSignature bool) (string, string) {
	if url == "" {
		return "", ""
	}
	existingExe, existingData, marker, hasExisting := voacapget.Installed(destDir)

	log.Printf("voacapl: checking %s", url)
	// Probe the (tiny) checksum sidecar with a short timeout so an offline
	// machine does not sit at a blank window for minutes.
	sumCtx, cancelSum := context.WithTimeout(context.Background(), 10*time.Second)
	sum, err := voacapget.RemoteSHA256(sumCtx, url+".sha256")
	cancelSum()
	if err != nil {
		if hasExisting {
			log.Printf("voacapl: cannot check for updates (%v); using existing install at %s", err, destDir)
			return existingExe, existingData
		}
		log.Printf("voacapl: cannot verify download: %v", err)
		return "", ""
	}
	if hasExisting && strings.EqualFold(marker, sum) {
		log.Printf("voacapl: using existing install at %s", destDir)
		return existingExe, existingData
	}

	var opts []voacapget.Option
	if requireSignature {
		sigCtx, cancelSig := context.WithTimeout(context.Background(), 10*time.Second)
		sig, sigErr := voacapget.RemoteFile(sigCtx, url+".minisig", 64<<10)
		cancelSig()
		if sigErr != nil {
			if hasExisting {
				log.Printf("voacapl: cannot fetch bundle signature (%v); using existing install", sigErr)
				return existingExe, existingData
			}
			log.Printf("voacapl: cannot verify bundle signature: %v", sigErr)
			return "", ""
		}
		opts = append(opts, voacapget.WithMinisignSignature(sig))
	}

	log.Printf("voacapl: downloading %s", url)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	exe, data, err := voacapget.Fetch(ctx, url, destDir, sum, opts...)
	if err != nil {
		log.Printf("voacapl: download failed: %v", err)
		if hasExisting {
			return existingExe, existingData
		}
		return "", ""
	}
	log.Printf("voacapl ready: %s (data %s)", exe, data)
	return exe, data
}

func defaultServiceCodesCSV() string {
	return strings.Join(cmsapi.DefaultServiceCodes, ",")
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v, ok := os.LookupEnv(key); ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
