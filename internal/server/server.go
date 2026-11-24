package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ben-kuhn/rms-radar/internal/browser"
	"github.com/ben-kuhn/rms-radar/internal/catalog"
	"github.com/ben-kuhn/rms-radar/internal/cmsapi"
	"github.com/ben-kuhn/rms-radar/internal/propagation"
	"github.com/ben-kuhn/rms-radar/internal/radiopost"
)

// Config configures the HTTP server.
type Config struct {
	Version           string
	VOACAPURL         string
	VOACAPExecutable  string
	VOACAPDataDir     string
	TileURL           string
	TileCacheDir      string
	TileCacheMaxBytes int64
	VHFMaxRangeKm     float64
	// RadioPost serves the separate RadioPost directory; nil disables it.
	RadioPost *radiopost.Loader
	// ServiceCodesPath persists the runtime-configurable service-code list.
	ServiceCodesPath string
	// AdminToken, when set, is required in the X-RMS-Radar-Admin header to
	// change service codes. It also enables the endpoint on server builds.
	AdminToken string
	// OpenURL opens an external documentation page. It defaults to the system
	// browser; tests can replace it.
	OpenURL func(string) error
	// DesktopMode enables service-code editing from the same-origin UI without
	// an admin token. It is set only by the native and browser desktop modes.
	DesktopMode bool

	// DesktopToken enables the desktop lifecycle endpoints. It is non-empty
	// only when the app was launched in desktop (double-click) mode.
	DesktopToken string
	// OnQuit asks the process to exit (the desktop window/tab is gone, or the
	// user pressed Quit).
	OnQuit func()
}

// Server serves the web UI and JSON API.
type Server struct {
	cfg       Config
	web       fs.FS
	loader    *cmsapi.Loader
	radiopost *radiopost.Loader
	heuristic *propagation.Heuristic
	ranker    *propagation.Ranker

	stationsMu sync.RWMutex
	stations   []catalog.Station

	voacapEnabled bool

	// assetVersion is a short content hash of the mutable web assets, used to
	// cache-bust app.js/style.css behind a CDN.
	assetVersion string

	tileClient *http.Client

	// Global admission + tile cache state.
	rankSem      chan struct{}
	tileSem      chan struct{}
	tileLimiter  *ipLimiter
	rankClients  *clientGate
	tileMu       sync.Mutex
	tileInFlight map[string]chan struct{}
	tileBytes    int64
	tileMaxBytes int64
	tileEvicting bool

	// refreshCodes reloads the gateway data after the service-code list changes.
	refreshCodes func(context.Context) error

	// Service-code endpoint admission: a global cooldown and a single-flight
	// refresh prevent a burst of POSTs from piling up upstream fetches.
	codeChangeMu          sync.Mutex
	lastCodeChange        time.Time
	codeChangeMinInterval time.Duration
	codeRefreshSem        chan struct{}
	refreshMu             sync.Mutex
	refreshCooldown       time.Duration
	lastManualRefresh     time.Time

	// Desktop (double-click) lifecycle: the page checks in while it is open,
	// and the process exits once every page has gone away.
	desktopMu      sync.Mutex
	desktopClients map[string]time.Time
	desktopSeen    bool
	desktopEmptyAt time.Time
}

func New(cfg Config, web fs.FS, loader *cmsapi.Loader, heur *propagation.Heuristic) *Server {
	if cfg.OpenURL == nil {
		cfg.OpenURL = browser.Open
	}
	// More accurate predictors first; the always-available heuristic is last.
	var predictors []propagation.Predictor
	voacapEnabled := false
	if cfg.VOACAPURL != "" {
		predictors = append(predictors, propagation.NewVOACAPAPI(cfg.VOACAPURL))
		voacapEnabled = true
	}
	if cfg.VOACAPExecutable != "" {
		if p, err := propagation.NewVOACAP(cfg.VOACAPExecutable, cfg.VOACAPDataDir); err == nil {
			predictors = append(predictors, p)
			voacapEnabled = true
		}
	}
	predictors = append(predictors, heur)

	s := &Server{
		cfg:           cfg,
		web:           web,
		loader:        loader,
		radiopost:     cfg.RadioPost,
		heuristic:     heur,
		ranker:        &propagation.Ranker{Predictors: predictors},
		voacapEnabled: voacapEnabled,
		assetVersion:  assetVersion(web),
		tileClient:    &http.Client{Timeout: 15 * time.Second},

		rankSem:      make(chan struct{}, 2),
		tileSem:      make(chan struct{}, 8),
		tileLimiter:  newIPLimiter(20, 60),
		rankClients:  newClientGate(),
		tileInFlight: make(map[string]chan struct{}),
		tileMaxBytes: cfg.TileCacheMaxBytes,

		codeChangeMinInterval: time.Second,
		codeRefreshSem:        make(chan struct{}, 1),
		refreshCooldown:       5 * time.Minute,

		desktopClients: make(map[string]time.Time),
	}
	s.refreshCodes = func(ctx context.Context) error {
		if err := s.loader.Refresh(ctx, true); err != nil {
			return err
		}
		return s.Refresh(ctx)
	}
	if s.tileMaxBytes <= 0 {
		s.tileMaxBytes = 512 << 20
	}
	if cfg.TileCacheDir != "" {
		s.tileBytes = dirSize(cfg.TileCacheDir)
	}
	if s.cfg.DesktopToken != "" {
		go s.monitorDesktop()
	}
	return s
}

// assetVersion returns a short hash of the mutable web assets. When app.js,
// theme.js, or style.css changes, the hash changes and the HTML points at a new
// URL, which bypasses CDN edge caches (which ignore a browser hard-refresh).
func assetVersion(web fs.FS) string {
	h := sha256.New()
	for _, name := range []string{"app.js", "theme.js", "style.css", "vendor/leaflet/leaflet.js", "vendor/leaflet/leaflet.css"} {
		if b, err := fs.ReadFile(web, name); err == nil {
			_, _ = h.Write(b)
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:8]
}

// Refresh re-parses the loader's current snapshot into the normalized catalog.
func (s *Server) Refresh(ctx context.Context) error {
	data, _, _ := s.loader.Snapshot()
	if len(data) == 0 {
		return nil
	}
	if err := cmsapi.ValidateGatewayStatus(data); err != nil {
		return err
	}
	var status cmsapi.GatewayStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return err
	}
	stations := catalog.Normalize(&status)
	if len(stations) == 0 {
		return errors.New("gateway feed produced no stations")
	}
	s.stationsMu.Lock()
	s.stations = stations
	s.stationsMu.Unlock()
	return nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/gateways", s.handleGateways)
	mux.HandleFunc("/api/servicecodes", s.handleServiceCodes)
	mux.HandleFunc("/api/servicecodes/config", s.handleServiceCodesConfig)
	mux.HandleFunc("/api/conditions", s.handleConditions)
	mux.HandleFunc("/api/rank", s.handleRank)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/refresh", s.handleRefresh)
	if s.radiopost != nil {
		mux.HandleFunc("/api/radiopost/gateways", s.handleRadioPostGateways)
		mux.HandleFunc("/api/radiopost/refresh", s.handleRadioPostRefresh)
	}
	mux.HandleFunc("/api/desktop/hello", s.handleDesktopHello)
	mux.HandleFunc("/api/desktop/beat", s.handleDesktopBeat)
	mux.HandleFunc("/api/desktop/bye", s.handleDesktopBye)
	mux.HandleFunc("/api/desktop/quit", s.handleDesktopQuit)
	mux.HandleFunc("/api/desktop/open", s.handleDesktopOpen)
	mux.HandleFunc("/tiles/", s.handleTiles)

	fileServer := http.FileServer(http.FS(s.web))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/", "/index.html":
			s.serveHTML(w, r, "index.html")
		case "/clients.html":
			s.serveHTML(w, r, "clients.html")
		case "/gateway.html":
			s.serveHTML(w, r, "gateway.html")
		default:
			// Content-hashed (?v=) assets can be cached for a few days: a new
			// build changes the hash, so the HTML always points at fresh URLs.
			// Unversioned files get a short TTL so a stale copy can't linger.
			if r.URL.Query().Get("v") != "" {
				w.Header().Set("Cache-Control", "public, max-age=259200") // 3 days
			} else {
				w.Header().Set("Cache-Control", "public, max-age=300")
			}
			fileServer.ServeHTTP(w, r)
		}
	})
	return securityHeaders(mux)
}

// serveHTML renders an HTML page with content-hashed asset URLs and no-cache,
// so a fresh page always points at fresh JS/CSS even behind an aggressive CDN.
func (s *Server) serveHTML(w http.ResponseWriter, r *http.Request, page string) {
	b, err := fs.ReadFile(s.web, page)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	html := string(b)
	if s.assetVersion != "" {
		v := s.assetVersion
		html = strings.ReplaceAll(html, `href="style.css"`, `href="style.css?v=`+v+`"`)
		html = strings.ReplaceAll(html, `src="app.js"`, `src="app.js?v=`+v+`"`)
		html = strings.ReplaceAll(html, `src="theme.js"`, `src="theme.js?v=`+v+`"`)
		html = strings.ReplaceAll(html, `href="vendor/leaflet/leaflet.css"`, `href="vendor/leaflet/leaflet.css?v=`+v+`"`)
		html = strings.ReplaceAll(html, `src="vendor/leaflet/leaflet.js"`, `src="vendor/leaflet/leaflet.js?v=`+v+`"`)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(html))
}

// securityHeaders adds defensive headers to every response. The CSP allows the
// pinned OSM/CARTO tile hosts and Leaflet's inline styles.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' https:; "+
				"connect-src 'self'; font-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) snapshotStations() []catalog.Station {
	s.stationsMu.RLock()
	defer s.stationsMu.RUnlock()
	return s.stations
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if !getOnly(w, r) {
		return
	}
	_, source, at := s.loader.Snapshot()
	stations := s.snapshotStations()
	writeJSON(w, map[string]any{
		"ok":            true,
		"version":       s.cfg.Version,
		"source":        source.String(),
		"fetched_at":    at.UTC().Format(time.RFC3339),
		"stations":      len(stations),
		"service_codes": s.loader.ServiceCodes(),
		"stale":         s.loader.LastError() != "",
		"last_error":    s.loader.LastError(),
	})
}

func (s *Server) handleGateways(w http.ResponseWriter, r *http.Request) {
	if !getOnly(w, r) {
		return
	}
	writeJSON(w, s.snapshotStations())
}

func (s *Server) handleServiceCodes(w http.ResponseWriter, r *http.Request) {
	if !getOnly(w, r) {
		return
	}
	counts := map[string]int{}
	for _, st := range s.snapshotStations() {
		for _, ch := range st.Channels {
			sc := ch.ServiceCode
			if sc == "" {
				sc = "PUBLIC"
			}
			counts[sc]++
		}
	}
	type scEntry struct {
		Code  string `json:"code"`
		Count int    `json:"count"`
	}
	list := make([]scEntry, 0, len(counts))
	for k, v := range counts {
		list = append(list, scEntry{Code: k, Count: v})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Count > list[j].Count })
	writeJSON(w, list)
}

func (s *Server) handleConditions(w http.ResponseWriter, r *http.Request) {
	if !getOnly(w, r) {
		return
	}
	writeJSON(w, s.heuristic.Conditions())
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if !getOnly(w, r) {
		return
	}
	writeJSON(w, map[string]any{
		"version":                      s.cfg.Version,
		"service_codes":                s.loader.ServiceCodes(),
		"default_service_codes":        append([]string(nil), cmsapi.DefaultServiceCodes...),
		"service_codes_editable":       s.serviceCodeEditingEnabled(),
		"service_codes_token_required": s.serviceCodeTokenRequired(),
		"desktop":                      s.cfg.DesktopMode || s.cfg.DesktopToken != "",
		"voacap":                       s.voacapEnabled,
		"tile_url":                     proxyTilePath,
		"radiopost":                    s.radiopost != nil,
	})
}

func getOnly(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	// Gateway lists and propagation data are refreshed from Winlink/upstream and
	// must never be cached (by the browser or a CDN).
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}
