package server

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"math"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ben-kuhn/rms-radar/internal/catalog"
	"github.com/ben-kuhn/rms-radar/internal/propagation"
)

// rankedEntry is one (station, channel) candidate in a ranked result.
type rankedEntry struct {
	Callsign          string  `json:"callsign"`
	Latitude          float64 `json:"latitude"`
	Longitude         float64 `json:"longitude"`
	Gridsquare        string  `json:"gridsquare"`
	ServiceCode       string  `json:"service_code"`
	Mode              string  `json:"mode"`
	ModeFamily        string  `json:"mode_family"`
	Band              string  `json:"band"`
	FrequencyHz       float64 `json:"frequency_hz"`
	Frequency         string  `json:"frequency"`
	CenterFrequencyHz float64 `json:"center_frequency_hz"`
	CenterFrequency   string  `json:"center_frequency"`
	DistanceKm        float64 `json:"distance_km"`
	BearingDeg        float64 `json:"bearing_deg"`
	LinkQuality       int     `json:"link_quality"`
	BandOpen          bool    `json:"band_open"`
	MUF               float64 `json:"muf_mhz,omitempty"`
	Method            string  `json:"method"`
	Note              string  `json:"note,omitempty"`
}

// Per-request rank deadlines. app.go sets WriteTimeout to
// RankTimeoutVOACAP + a margin, so the HTTP write always outlives the rank.
const (
	RankTimeoutHeuristic = 120 * time.Second
	RankTimeoutVOACAP    = 5 * time.Minute
)

// maxRankCandidates bounds a single rank request; a whole-feed rank (all
// bands/modes) is a self-DoS, especially when each candidate runs VOACAP.
const maxRankCandidates = 500

func (s *Server) handleRank(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var rr struct {
		Grid        string `json:"grid"`
		Band        string `json:"band"`
		Mode        string `json:"mode"`
		ServiceCode string `json:"service_code"`
	}
	body := http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(body)
	if err := dec.Decode(&rr); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		http.Error(w, "unexpected trailing data", http.StatusBadRequest)
		return
	}
	grid, band, mode, sc := rr.Grid, rr.Band, rr.Mode, rr.ServiceCode

	from, err := propagation.ParseLocator(grid)
	if err != nil {
		http.Error(w, "invalid grid locator: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Fairness: one active ranking per client. This keeps a single client from
	// holding both global slots for the full VOACAP deadline.
	client := clientKey(r)
	if !s.rankClients.acquire(client) {
		w.Header().Set("Retry-After", "5")
		http.Error(w, "ranking is already running for this client", http.StatusTooManyRequests)
		return
	}
	defer s.rankClients.release(client)

	// Global admission: concurrent ranking requests each spawn workers and can
	// run up to 500 predictions (or VOACAP subprocesses). Reject excess load
	// instead of multiplying it.
	select {
	case s.rankSem <- struct{}{}:
		defer func() { <-s.rankSem }()
	default:
		w.Header().Set("Retry-After", "10")
		http.Error(w, "ranking is busy; try again shortly", http.StatusTooManyRequests)
		return
	}

	ssn := s.heuristic.Conditions().Sunspots

	type cand struct {
		st   catalog.Station
		ch   catalog.Channel
		dist float64
	}
	var cands []cand
	for _, st := range s.snapshotStations() {
		to := propagation.Point{Lat: st.Latitude, Lon: st.Longitude}
		d := propagation.DistanceKm(from, to)
		for _, ch := range st.Channels {
			if sc != "" && !strings.EqualFold(ch.ServiceCode, sc) {
				continue
			}
			if band != "" && ch.Band != band {
				continue
			}
			if mode != "" && !strings.EqualFold(ch.ModeFamily, mode) {
				continue
			}
			// VHF/UHF is line-of-sight. This is a distance-based sanity bound
			// (not continent-based), so nearby cross-border stations still
			// appear while implausibly distant paths are dropped.
			if ch.FrequencyHz >= 30e6 && d > s.vhfMaxRangeKm(parseRangeKm(ch.RadioRange)) {
				continue
			}
			cands = append(cands, cand{st: st, ch: ch, dist: d})
		}
	}

	// Keep the nearest candidates when capping. Truncating in callsign order
	// used to drop the closest stations and keep arbitrary distant ones.
	sort.Slice(cands, func(i, j int) bool { return cands[i].dist < cands[j].dist })
	truncated := len(cands) > maxRankCandidates
	if truncated {
		cands = cands[:maxRankCandidates]
		w.Header().Set("X-Rank-Truncated", "true")
		log.Printf("rank: truncated to %d candidates; narrow band/mode for complete results", maxRankCandidates)
	}

	timeout := RankTimeoutHeuristic
	if s.voacapEnabled {
		timeout = RankTimeoutVOACAP
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	out := make([]rankedEntry, len(cands))
	ok := make([]bool, len(cands))
	workers := runtime.NumCPU()
	if workers < 1 {
		workers = 1
	}
	if workers > 4 {
		workers = 4
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				c := cands[idx]
				pred, err := s.ranker.Predict(ctx, propagation.Params{
					FromLat:      from.Lat,
					FromLon:      from.Lon,
					ToLat:        c.st.Latitude,
					ToLon:        c.st.Longitude,
					FrequencyHz:  c.ch.FrequencyHz,
					Mode:         c.ch.ModeFamily,
					RadioRangeKm: parseRangeKm(c.ch.RadioRange),
					SSN:          ssn,
				})
				if err != nil {
					continue
				}
				ok[idx] = true
				out[idx] = rankedEntry{
					Callsign:          c.st.Callsign,
					Latitude:          c.st.Latitude,
					Longitude:         c.st.Longitude,
					Gridsquare:        c.ch.Gridsquare,
					ServiceCode:       c.ch.ServiceCode,
					Mode:              c.ch.Mode,
					ModeFamily:        c.ch.ModeFamily,
					Band:              c.ch.Band,
					FrequencyHz:       c.ch.FrequencyHz,
					Frequency:         c.ch.Frequency,
					CenterFrequencyHz: c.ch.CenterFrequencyHz,
					CenterFrequency:   c.ch.CenterFrequency,
					DistanceKm:        pred.DistanceKm,
					BearingDeg:        pred.BearingDeg,
					LinkQuality:       pred.LinkQuality,
					BandOpen:          pred.BandOpen,
					MUF:               pred.MUF,
					Method:            pred.Method,
					Note:              pred.Note,
				}
			}
		}()
	}
dispatch:
	for i := range cands {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break dispatch
		}
	}
	close(jobs)
	wg.Wait()
	if ctx.Err() != nil {
		http.Error(w, "ranking timed out", http.StatusServiceUnavailable)
		return
	}

	results := make([]rankedEntry, 0, len(out))
	failed := 0
	for i := range out {
		if ok[i] {
			results = append(results, out[i])
		} else {
			failed++
		}
	}
	if failed > 0 {
		w.Header().Set("X-Rank-Errors", strconv.Itoa(failed))
		log.Printf("rank: %d of %d candidates failed", failed, len(out))
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].LinkQuality != results[j].LinkQuality {
			return results[i].LinkQuality > results[j].LinkQuality
		}
		return results[i].DistanceKm < results[j].DistanceKm
	})
	writeJSON(w, results)
}

// parseRangeKm parses the gateway's advertised radio range. Winlink reports
// this in miles; we convert to km (the exact unit matters little for ranking).
func parseRangeKm(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f <= 0 {
		return 0
	}
	return f * 1.60934
}

// vhfMaxRangeKm returns the maximum plausible distance for a VHF/UHF
// (line-of-sight) path. This is purely distance-based (not continent-based), so
// e.g. a station at Gibraltar still sees nearby Europe and Africa. The floor is
// configurable (-vhf-max-range); set it large to effectively disable the filter.
func (s *Server) vhfMaxRangeKm(advertisedKm float64) float64 {
	floor := s.cfg.VHFMaxRangeKm
	if floor <= 0 {
		floor = 400 // default
	}
	return math.Max(floor, advertisedKm*1.5)
}
