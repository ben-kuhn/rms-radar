package server

import (
	"mime"
	"net/http"
)

// handleRadioPostGateways returns the normalized RadioPost directory. It is a
// separate source from the Winlink gateway feed and never includes Winlink
// service codes.
func (s *Server) handleRadioPostGateways(w http.ResponseWriter, r *http.Request) {
	if !getOnly(w, r) {
		return
	}
	if s.radiopost == nil {
		http.Error(w, "RadioPost is unavailable", http.StatusServiceUnavailable)
		return
	}
	stations, _, err := s.radiopost.Snapshot(r.Context(), false)
	if err != nil && len(stations) == 0 {
		http.Error(w, "RadioPost directory unavailable", http.StatusBadGateway)
		return
	}
	writeJSON(w, stations)
}

// handleRadioPostRefresh forces a directory/status refresh, subject to the
// loader's one-minute status-poll floor. Like the Winlink refresh endpoint, it
// requires a same-origin JSON POST.
func (s *Server) handleRadioPostRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	if !originHostMatches(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if s.radiopost == nil {
		http.Error(w, "RadioPost is unavailable", http.StatusServiceUnavailable)
		return
	}
	stations, _, err := s.radiopost.Snapshot(r.Context(), true)
	if err != nil && len(stations) == 0 {
		http.Error(w, "RadioPost refresh failed", http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"status": "refreshed"})
}
