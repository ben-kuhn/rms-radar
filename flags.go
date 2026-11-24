package main

import (
	"flag"
	"time"

	"github.com/ben-kuhn/rms-radar/internal/cmsapi"
)

// commonFlags are shared by the web/server and native desktop entry points.
// Defining them once keeps the two binaries from drifting (the listen/cache
// defaults and the desktop-only -voacap-auto default are passed in).
type commonFlags struct {
	Listen               *string
	ServiceCodes         *string
	Refresh              *time.Duration
	RadioPostRefresh     *time.Duration
	CacheDir             *string
	AccessKey            *string
	HistoryHours         *int
	VOACAPURL            *string
	VOACAPExecutable     *string
	VOACAPDataDir        *string
	VOACAPDownloadURL    *string
	VOACAPDownloadDir    *string
	VOACAPDownloadSHA256 *string
	AdminToken           *string
	VOACAPAuto           *bool
	WebDir               *string
	TileURL              *string
	TileCacheMax         *int
	VHFRange             *float64
}

func registerCommonFlags(listenDefault, cacheDefault, listenHelp, cacheHelp string, autoDefault bool) *commonFlags {
	return &commonFlags{
		Listen:           flag.String("listen", envOr("RMSRADAR_LISTEN", listenDefault), listenHelp),
		ServiceCodes:     flag.String("service-codes", envOr("RMSRADAR_SERVICE_CODES", defaultServiceCodesCSV()), "comma-separated Winlink service codes to fetch (the API accepts explicit codes; there is no wildcard)"),
		Refresh:          flag.Duration("refresh", envDuration("RMSRADAR_REFRESH", 24*time.Hour), "Winlink gateway list refresh interval (daily by default; the UI Refresh button forces an update)"),
		RadioPostRefresh: flag.Duration("radiopost-refresh", envDuration("RMSRADAR_RADIOPOST_REFRESH", 10*time.Minute), "RadioPost directory/status poll interval (minimum 1m)"),
		CacheDir:         flag.String("cache-dir", envOr("RMSRADAR_CACHE_DIR", cacheDefault), cacheHelp),
		AccessKey:        flag.String("key", envOr("RMSRADAR_KEY", cmsapiDefaultAccessKey()), "Winlink CMS access key"),
		HistoryHours:     flag.Int("history-hours", envInt("RMSRADAR_HISTORY_HOURS", 48), "hours of channel history to fetch (1-48)"),

		VOACAPURL:            flag.String("voacap-url", envOr("RMSRADAR_VOACAP_URL", ""), "optional remote VOACAP API base URL (enables forecasts when set)"),
		VOACAPExecutable:     flag.String("voacap-executable", envOr("RMSRADAR_VOACAP_EXECUTABLE", ""), "path to a local voacapl/voacapw binary (enables forecasts)"),
		VOACAPDataDir:        flag.String("voacap-data-dir", envOr("RMSRADAR_VOACAP_DATA_DIR", ""), "ITSHFBC data directory for the local VOACAP binary"),
		VOACAPDownloadURL:    flag.String("voacap-download-url", envOr("RMSRADAR_VOACAP_DOWNLOAD_URL", ""), "download a VOACAP distribution (binary + itshfbc data) from this URL (opt-in; no package manager required)"),
		VOACAPDownloadDir:    flag.String("voacap-download-dir", envOr("RMSRADAR_VOACAP_DOWNLOAD_DIR", defaultVOACAPDownloadDir()), "directory to extract a downloaded VOACAP distribution into"),
		VOACAPDownloadSHA256: flag.String("voacap-download-sha256", envOr("RMSRADAR_VOACAP_DOWNLOAD_SHA256", ""), "optional SHA-256 of the VOACAP distribution archive"),
		AdminToken:           flag.String("admin-token", envOr("RMSRADAR_ADMIN_TOKEN", ""), "admin token required in the X-RMS-Radar-Admin header to change service codes (also enables the endpoint in server mode)"),
		VOACAPAuto:           flag.Bool("voacap-auto", envBool("RMSRADAR_VOACAP_AUTO", autoDefault), "automatically download the RMS Radar VOACAP bundle when no engine is installed"),

		WebDir:       flag.String("web-dir", envOr("RMSRADAR_WEB_DIR", ""), "serve web assets from this directory instead of the embedded copy"),
		TileURL:      flag.String("tile-url", envOr("RMSRADAR_TILE_URL", "https://tile.openstreetmap.org/{z}/{x}/{y}.png"), "upstream tile URL template proxied+cached by the server (OSM by default)"),
		TileCacheMax: flag.Int("tile-cache-max", envInt("RMSRADAR_TILE_CACHE_MAX_MB", 512), "tile cache quota in MiB (set a very large value to disable eviction)"),
		VHFRange:     flag.Float64("vhf-max-range", envFloat("RMSRADAR_VHF_MAX_RANGE", 400), "max plausible VHF/UHF (>=30 MHz) path length in km for ranking (distance-based; set large to effectively disable)"),
	}
}

// cmsapiDefaultAccessKey keeps the import of cmsapi out of flags.go's public
// surface while preserving the single shared default.
func cmsapiDefaultAccessKey() string {
	return cmsapi.DefaultAccessKey
}
