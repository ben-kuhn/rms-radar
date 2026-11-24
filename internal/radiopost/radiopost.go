// Package radiopost fetches and normalizes the RadioPost gateway directory and
// signed live-status lists. RadioPost is a separate network from Winlink, so
// its stations never share Winlink service codes.
package radiopost

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ben-kuhn/rms-radar/internal/catalog"
)

const (
	DefaultDirectoryURL = "https://radiopost.org/data/directory.json"
	DefaultStatusURL    = "https://po.radiopost.org/rp/v1/gateways/status"

	DefaultRefreshInterval = 10 * time.Minute
	MinStatusPollInterval  = time.Minute
	directoryMaxAge        = 6 * time.Hour
	maxDocumentBytes       = 4 << 20
)

var modemLabels = map[string]string{
	"aether":    "Aether",
	"aether-fm": "Aether FM",
	"ardop":     "ARDOP",
	"ax25":      "AX.25",
	"mercury":   "Mercury",
	"vara-hf":   "VARA HF",
	"vara-fm":   "VARA FM",
}

// Loader caches the directory and polls each post office's status list. The
// status list is polled at most once per configured interval, with a hard
// floor of once per minute as required by RadioPost.
type Loader struct {
	DirectoryURL    string
	StatusURL       string
	RefreshInterval time.Duration
	HTTP            *http.Client

	mu        sync.Mutex
	stations  []catalog.Station
	fetchedAt time.Time
	directory *directory
	dirAt     time.Time
	statusAt  time.Time
	lastErr   error
}

func NewLoader(refresh time.Duration) *Loader {
	if refresh <= 0 {
		refresh = DefaultRefreshInterval
	}
	if refresh < MinStatusPollInterval {
		refresh = MinStatusPollInterval
	}
	return &Loader{
		DirectoryURL:    DefaultDirectoryURL,
		StatusURL:       DefaultStatusURL,
		RefreshInterval: refresh,
		HTTP:            &http.Client{Timeout: 20 * time.Second},
	}
}

// Run polls in the background until ctx is cancelled.
func (l *Loader) Run(ctx context.Context) {
	_, _, _ = l.Snapshot(ctx, false)
	ticker := time.NewTicker(l.RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _, _ = l.Snapshot(ctx, true)
		}
	}
}

// Snapshot returns normalized RadioPost stations, fetching when the cache is
// older than RefreshInterval. force bypasses RefreshInterval but still respects
// the one-minute status-poll floor. On a fetch failure the last good snapshot is
// returned with the error.
func (l *Loader) Snapshot(ctx context.Context, force bool) ([]catalog.Station, time.Time, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	if !force && len(l.stations) > 0 && now.Sub(l.fetchedAt) < l.RefreshInterval {
		return l.stations, l.fetchedAt, nil
	}
	if !l.statusAt.IsZero() && now.Sub(l.statusAt) < MinStatusPollInterval {
		return l.stations, l.fetchedAt, l.lastErr
	}

	if l.directory == nil || now.Sub(l.dirAt) >= directoryMaxAge {
		dir, err := l.fetchDirectory(ctx)
		if err != nil {
			l.lastErr = err
			if len(l.stations) > 0 {
				return l.stations, l.fetchedAt, err
			}
			return nil, time.Time{}, err
		}
		l.directory = dir
		l.dirAt = now
	}

	statuses, err := l.fetchStatuses(ctx, l.directory)
	if err != nil && len(statuses) == 0 {
		l.lastErr = err
		if len(l.stations) > 0 {
			return l.stations, l.fetchedAt, err
		}
		return nil, time.Time{}, err
	}
	stations := Normalize(l.directory, statuses, now)
	l.stations = stations
	l.fetchedAt = now
	l.statusAt = now
	l.lastErr = nil
	return l.stations, l.fetchedAt, nil
}

func (l *Loader) fetchDirectory(ctx context.Context) (*directory, error) {
	var d directory
	if err := l.getJSON(ctx, l.DirectoryURL, &d); err != nil {
		return nil, fmt.Errorf("radiopost directory: %w", err)
	}
	if len(d.Gateways) == 0 {
		return nil, fmt.Errorf("radiopost directory: no gateways")
	}
	return &d, nil
}

func (l *Loader) fetchStatuses(ctx context.Context, dir *directory) (map[string]liveStatus, error) {
	urls := dir.StatusURLs()
	if len(urls) == 0 {
		urls = []string{l.StatusURL}
	}
	out := make(map[string]liveStatus)
	var lastErr error
	for _, u := range urls {
		b, err := l.get(ctx, u, "application/jose")
		if err != nil {
			lastErr = err
			continue
		}
		doc, err := decodeJWS(b)
		if err != nil {
			lastErr = err
			continue
		}
		for _, entry := range doc.Status {
			ls := liveStatus{seen: entry.Seen, reported: entry.Reported, up: map[string]bool{}}
			if entry.Report != nil {
				for _, ch := range entry.Report.Channels {
					ls.up[statusKey(ch.DialHz, ch.Modem)] = ch.Up
				}
			}
			if old, ok := out[strings.ToUpper(entry.Callsign)]; !ok || ls.seen > old.seen {
				out[strings.ToUpper(entry.Callsign)] = ls
			}
		}
	}
	if len(out) == 0 && lastErr != nil {
		return nil, lastErr
	}
	return out, nil
}

func (l *Loader) getJSON(ctx context.Context, url string, dst any) error {
	b, err := l.get(ctx, url, "application/json")
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

func (l *Loader) get(ctx context.Context, url, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "rms-radar (+https://github.com/ben-kuhn/rms-radar)")
	req.Header.Set("Accept", accept)
	resp, err := l.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxDocumentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxDocumentBytes {
		return nil, fmt.Errorf("%s: response too large", url)
	}
	return b, nil
}

type directory struct {
	Schema    string    `json:"schema"`
	Generated string    `json:"generated"`
	Sources   []source  `json:"sources"`
	Gateways  []gateway `json:"gateways"`
}

type source struct {
	Domain string `json:"domain"`
	API    string `json:"api"`
}

func (d *directory) StatusURLs() []string {
	var out []string
	for _, s := range d.Sources {
		if strings.TrimSpace(s.API) != "" {
			out = append(out, strings.TrimRight(s.API, "/")+"/gateways/status")
		}
	}
	return out
}

type gateway struct {
	Callsign   string    `json:"callsign"`
	Operator   string    `json:"operator"`
	PostOffice string    `json:"post_office"`
	Grid       string    `json:"grid"`
	Cell       *cell     `json:"cell"`
	Place      string    `json:"place"`
	Hours      string    `json:"hours"`
	Channels   []channel `json:"channels"`
	Test       bool      `json:"test"`
	Seen       int64     `json:"seen"`
}

type cell struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type channel struct {
	DialHz      float64 `json:"dial_hz"`
	Band        string  `json:"band"`
	Mode        string  `json:"mode"`
	Modem       string  `json:"modem"`
	BandwidthHz int     `json:"bandwidth_hz"`
	ScanS       int     `json:"scan_s"`
}

type statusDocument struct {
	Issued int64 `json:"issued"`
	Status []struct {
		Callsign string `json:"callsign"`
		Seen     int64  `json:"seen"`
		Reported int64  `json:"reported"`
		Report   *struct {
			State    string `json:"state"`
			Channels []struct {
				DialHz float64 `json:"dial_hz"`
				Modem  string  `json:"modem"`
				Up     bool    `json:"up"`
			} `json:"channels"`
		} `json:"report"`
	} `json:"status"`
}

type liveStatus struct {
	seen     int64
	reported int64
	up       map[string]bool
}

func decodeJWS(b []byte) (*statusDocument, error) {
	parts := strings.Split(strings.TrimSpace(string(b)), ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("radiopost status: not a JWS")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("radiopost status: %w", err)
	}
	var doc statusDocument
	if err := json.Unmarshal(payload, &doc); err != nil {
		return nil, fmt.Errorf("radiopost status: %w", err)
	}
	return &doc, nil
}

func statusKey(hz float64, modem string) string {
	return fmt.Sprintf("%.0f/%s", hz, strings.ToLower(strings.TrimSpace(modem)))
}

// Normalize converts a directory and live-status map into the same station
// shape used by the Winlink map.
func Normalize(dir *directory, live map[string]liveStatus, now time.Time) []catalog.Station {
	stations := make([]catalog.Station, 0, len(dir.Gateways))
	for _, gw := range dir.Gateways {
		if gw.Test {
			continue
		}
		st := normalizeGateway(gw, live[strings.ToUpper(gw.Callsign)], now)
		if st.Callsign != "" {
			stations = append(stations, st)
		}
	}
	sort.Slice(stations, func(i, j int) bool { return stations[i].Callsign < stations[j].Callsign })
	return stations
}

func normalizeGateway(gw gateway, live liveStatus, now time.Time) catalog.Station {
	seen := gw.Seen
	if live.seen > seen {
		seen = live.seen
	}
	hours := -1
	if seen > 0 {
		hours = int(now.Sub(time.Unix(seen, 0)).Hours())
		if hours < 0 {
			hours = 0
		}
	}
	lat, lon := 0.0, 0.0
	if gw.Cell != nil {
		lat, lon = gw.Cell.Lat, gw.Cell.Lon
	}
	hoursText := strings.TrimSpace(gw.Hours)
	if hoursText == "" || strings.EqualFold(hoursText, "always") {
		hoursText = "00-23"
	}
	base := gw.Callsign
	if i := strings.Index(base, "-"); i > 0 {
		base = base[:i]
	}
	channels := make([]catalog.Channel, 0, len(gw.Channels))
	for _, ch := range gw.Channels {
		rawModem := strings.TrimSpace(ch.Modem)
		key := strings.ToLower(rawModem)
		if key == "tcp" || ch.DialHz <= 0 {
			continue
		}
		label, ok := modemLabels[key]
		if !ok {
			if rawModem == "" {
				label = "Unknown"
			} else {
				// Preserve a new modem name rather than hiding the channel.
				label = rawModem
			}
		}
		band := strings.TrimSpace(ch.Band)
		if band == "" {
			band = catalog.BandForFrequency(ch.DialHz)
		}
		channels = append(channels, catalog.Channel{
			Mode:              label,
			ModeFamily:        label,
			FrequencyHz:       ch.DialHz,
			Frequency:         catalog.FormatFrequency(ch.DialHz),
			CenterFrequencyHz: ch.DialHz,
			CenterFrequency:   catalog.FormatFrequency(ch.DialHz),
			Gridsquare:        gw.Grid,
			OperatingHours:    hoursText,
			ScanSeconds:       ch.ScanS,
			Band:              band,
		})
	}
	if len(channels) == 0 {
		return catalog.Station{}
	}
	last := ""
	if seen > 0 {
		last = time.Unix(seen, 0).UTC().Format(time.RFC3339)
	}
	return catalog.Station{
		Callsign:         gw.Callsign,
		BaseCallsign:     base,
		Latitude:         lat,
		Longitude:        lon,
		HoursSinceStatus: hours,
		LastStatus:       last,
		Comments:         gw.Place,
		Channels:         channels,
	}
}
