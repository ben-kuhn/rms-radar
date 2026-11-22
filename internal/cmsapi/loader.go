package cmsapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ben-kuhn/rms-radar/internal/atomicfile"
)

const (
	// MaxServiceCodes bounds the runtime code list so one request cannot force
	// an unbounded upstream query or cache key.
	MaxServiceCodes = 32
	// MaxServiceCodeLen matches the Winlink service-code alphabet.
	MaxServiceCodeLen = 16
)

var serviceCodeRE = regexp.MustCompile(`^[A-Z0-9_]{1,16}$`)

// DefaultServiceCodes is the public/community set fetched when the caller does
// not configure a list. Winlink exposes no wildcard and does not publish private
// group codes, so these are the known public/community codes; users can add a
// private group code in the desktop dialog or through the server admin API.
var DefaultServiceCodes = []string{"PUBLIC", "MERCURY", "VARDEFM", "MODEM73", "DATA2G", "IONHF", "XARA"}

// NormalizeServiceCodes trims, uppercases, validates, deduplicates, and sorts
// service codes. It rejects path separators, whitespace, and any other
// character that is not part of the Winlink service-code alphabet.
func NormalizeServiceCodes(codes []string) ([]string, error) {
	seen := make(map[string]bool, len(codes))
	out := make([]string, 0, len(codes))
	for _, c := range codes {
		c = strings.ToUpper(strings.TrimSpace(c))
		if c == "" {
			continue
		}
		if !serviceCodeRE.MatchString(c) {
			return nil, fmt.Errorf("invalid service code %q (use A-Z, 0-9, _; 1-%d characters)", c, MaxServiceCodeLen)
		}
		if seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	if len(out) > MaxServiceCodes {
		return nil, fmt.Errorf("too many service codes (max %d)", MaxServiceCodes)
	}
	sort.Strings(out)
	return out, nil
}

//go:embed gateway_status.json.gz
var embeddedGatewayStatus []byte

// Source describes where the current gateway data came from.
type Source int

const (
	SourceNetwork Source = iota
	SourceCache
	SourceEmbedded
)

func (s Source) String() string {
	switch s {
	case SourceNetwork:
		return "network"
	case SourceCache:
		return "cache"
	default:
		return "embedded"
	}
}

// Loader refreshes, caches, and serves the gateway status feed.
type Loader struct {
	client       *Client
	cacheDir     string
	historyHours int

	codesMu sync.RWMutex
	codes   []string

	refreshMu  sync.Mutex
	refreshing atomic.Bool

	mu        sync.RWMutex
	data      []byte
	source    Source
	fetchedAt time.Time
	lastErr   string
}

func NewLoader(cacheDir string, codes []string, historyHours int, key string) *Loader {
	normalized, err := NormalizeServiceCodes(codes)
	if err != nil || len(normalized) == 0 {
		normalized = append([]string(nil), DefaultServiceCodes...)
	}
	return &Loader{
		client:       NewClient(key),
		cacheDir:     cacheDir,
		codes:        normalized,
		historyHours: historyHours,
	}
}

func (l *Loader) cachePath() string {
	codes := l.ServiceCodes()
	if len(codes) == 1 && codes[0] == "PUBLIC" {
		return filepath.Join(l.cacheDir, "gateway_status.json")
	}
	// Never put user-supplied code text in a filename. The hash keeps the
	// cache key deterministic while making traversal impossible even if a
	// caller bypasses SetServiceCodes.
	sum := sha256.Sum256([]byte(strings.Join(codes, "\x00")))
	return filepath.Join(l.cacheDir, "gateway_status-"+hex.EncodeToString(sum[:])+".json")
}

// ServiceCodes returns a copy of the service codes currently requested.
func (l *Loader) ServiceCodes() []string {
	l.codesMu.RLock()
	defer l.codesMu.RUnlock()
	return append([]string(nil), l.codes...)
}

// SetServiceCodes validates and changes the requested service codes. The next
// Refresh (or Seed) uses the new cache key and network query. An invalid list
// leaves the existing codes unchanged.
func (l *Loader) SetServiceCodes(codes []string) error {
	normalized, err := NormalizeServiceCodes(codes)
	if err != nil {
		return err
	}
	if len(normalized) == 0 {
		return errors.New("at least one service code is required")
	}
	l.codesMu.Lock()
	defer l.codesMu.Unlock()
	l.codes = normalized
	return nil
}

// Seed loads data from the on-disk cache, or the embedded snapshot if there is
// no cache. It never touches the network, so it is safe to call at startup
// before serving. The caller can then kick off a forced network refresh in the
// background.
func (l *Loader) Seed() {
	if b, at, ok := l.readCached(); ok {
		l.set(b, SourceCache, at)
		return
	}
	if b, err := l.embedded(); err == nil && ValidateGatewayStatus(b) == nil {
		l.set(b, SourceEmbedded, time.Now())
	}
}

// readCached returns validated cached data and its modification time.
func (l *Loader) readCached() ([]byte, time.Time, bool) {
	path := l.cachePath()
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 || ValidateGatewayStatus(b) != nil {
		return nil, time.Time{}, false
	}
	at := time.Now()
	if st, err := os.Stat(path); err == nil {
		at = st.ModTime()
	}
	return b, at, true
}

// Refresh loads data preferring cache, then the network, then an embedded
// snapshot. When force is true the cache is skipped and the network is tried
// first (falling back to the embedded snapshot only on failure).
func (l *Loader) Refresh(ctx context.Context, force bool) error {
	// Serialize network refreshes so a UI-triggered refresh cannot race the
	// background ticker.
	l.refreshMu.Lock()
	defer l.refreshMu.Unlock()
	l.refreshing.Store(true)
	defer l.refreshing.Store(false)

	if !force {
		if b, at, ok := l.readCached(); ok {
			l.set(b, SourceCache, at)
			return nil
		}
	}

	b, fetchErr := l.client.Fetch(ctx, l.ServiceCodes(), l.historyHours)
	if fetchErr == nil {
		if l.cacheDir != "" {
			if err := atomicfile.Write(l.cachePath(), b, 0o644); err != nil {
				log.Printf("gateway cache write: %v", err)
			}
		}
		l.set(b, SourceNetwork, time.Now())
		return nil
	}

	// Keep the last-known-good snapshot; a failed refresh must not replace it
	// with a stale embedded copy.
	if cur, _, _ := l.Snapshot(); len(cur) > 0 {
		l.setError(fmt.Errorf("gateway fetch failed; keeping current data: %w", fetchErr))
		return fmt.Errorf("gateway fetch failed: %w", fetchErr)
	}
	if b, at, ok := l.readCached(); ok {
		l.set(b, SourceCache, at)
		return nil
	}
	if b, err := l.embedded(); err == nil && ValidateGatewayStatus(b) == nil {
		l.set(b, SourceEmbedded, time.Now())
		return nil
	}
	return fmt.Errorf("gateway fetch failed: %w", fetchErr)
}

func (l *Loader) embedded() ([]byte, error) {
	if len(embeddedGatewayStatus) == 0 {
		return nil, errors.New("no embedded gateway status")
	}
	zr, err := gzip.NewReader(bytes.NewReader(embeddedGatewayStatus))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}

func (l *Loader) set(b []byte, src Source, at time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.data = b
	l.source = src
	l.fetchedAt = at
	l.lastErr = ""
}

func (l *Loader) setError(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err == nil {
		l.lastErr = ""
	} else {
		l.lastErr = err.Error()
	}
}

// Refreshing reports whether a network/cache refresh is currently running.
func (l *Loader) Refreshing() bool { return l.refreshing.Load() }

// LastError reports why the most recent refresh failed, if it did. The last
// successful snapshot is kept.
func (l *Loader) LastError() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.lastErr
}

// Snapshot returns the current data, its source, and when it was loaded.
func (l *Loader) Snapshot() ([]byte, Source, time.Time) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.data, l.source, l.fetchedAt
}
