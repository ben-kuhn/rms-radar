package radiopost

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/ben-kuhn/rms-radar/internal/catalog"
)

func TestNormalize(t *testing.T) {
	dir := &directory{Gateways: []gateway{
		{
			Callsign: "KK4ODA-1", Grid: "EM73uu", Place: "Atlanta, GA", Hours: "always", Seen: 1000,
			Cell: &cell{Lat: 33.8542, Lon: -84.2917},
			Channels: []channel{
				{DialHz: 7101000, Band: "40m", Modem: "aether", ScanS: 54},
				{DialHz: 3590000, Band: "80m", Modem: "vara-hf"},
				{DialHz: 14107000, Band: "20m", Modem: "tcp"},
				{DialHz: 14108000, Band: "20m", Modem: "future"},
			},
		},
		{
			Callsign: "TEST-1", Test: true,
			Channels: []channel{{DialHz: 7101000, Band: "40m", Modem: "aether"}},
		},
	}}
	live := map[string]liveStatus{
		"KK4ODA-1": {seen: 1200, up: map[string]bool{statusKey(7101000, "aether"): true}},
	}
	stations := Normalize(dir, live, time.Unix(7200, 0))
	if len(stations) != 1 {
		t.Fatalf("stations = %d, want 1 (test gateway skipped)", len(stations))
	}
	st := stations[0]
	if st.Callsign != "KK4ODA-1" || st.BaseCallsign != "KK4ODA" {
		t.Fatalf("station identity = %#v", st)
	}
	if st.Latitude != 33.8542 || st.Longitude != -84.2917 {
		t.Errorf("cell = %v,%v", st.Latitude, st.Longitude)
	}
	if st.HoursSinceStatus != 1 {
		t.Errorf("hours since status = %d, want 1", st.HoursSinceStatus)
	}
	if st.LastStatus == "" || st.Comments != "Atlanta, GA" {
		t.Errorf("last status/comments = %q/%q", st.LastStatus, st.Comments)
	}
	if len(st.Channels) != 3 {
		t.Fatalf("channels = %d, want 3 (tcp skipped, unknown kept)", len(st.Channels))
	}
	if got := st.Channels[0].Mode; got != "Aether" {
		t.Errorf("aether mode = %q", got)
	}
	if got := st.Channels[1].Mode; got != "VARA HF" {
		t.Errorf("vara mode = %q", got)
	}
	if got := st.Channels[2].Mode; got != "future" {
		t.Errorf("unknown mode = %q, want raw modem name", got)
	}
	if st.Channels[0].ScanSeconds != 54 {
		t.Errorf("scan_s = %d, want 54", st.Channels[0].ScanSeconds)
	}
	if st.Channels[0].OperatingHours != "00-23" {
		t.Errorf("operating hours = %q", st.Channels[0].OperatingHours)
	}
	if st.Channels[0].FrequencyHz != 7101000 || st.Channels[0].Frequency != "7.101 MHz" {
		t.Errorf("frequency = %v %q", st.Channels[0].FrequencyHz, st.Channels[0].Frequency)
	}
	if st.Channels[0].CenterFrequencyHz != 7102500 || st.Channels[0].CenterFrequency != "7.1025 MHz" {
		t.Errorf("derived center = %v %q, want 7102500 / 7.1025 MHz", st.Channels[0].CenterFrequencyHz, st.Channels[0].CenterFrequency)
	}
}

func TestDecodeJWS(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{
		"issued": 123,
		"status": []map[string]any{{
			"callsign": "KK4ODA-1",
			"seen":     100,
			"report":   map[string]any{"state": "running"},
		}},
	})
	raw := "eyJhbGciOiJFZERTQSJ9." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
	doc, err := decodeJWS([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Issued != 123 || len(doc.Status) != 1 || doc.Status[0].Callsign != "KK4ODA-1" {
		t.Fatalf("decoded = %#v", doc)
	}
}

func TestStatusURLs(t *testing.T) {
	d := &directory{Sources: []source{
		{API: "https://po.example/rp/v1/"},
		{API: "https://other.example/api"},
	}}
	got := d.StatusURLs()
	if len(got) != 2 || got[0] != "https://po.example/rp/v1/gateways/status" || got[1] != "https://other.example/api/gateways/status" {
		t.Fatalf("status URLs = %v", got)
	}
}

func TestNewLoaderIntervalFloor(t *testing.T) {
	if got := NewLoader(0).RefreshInterval; got != DefaultRefreshInterval {
		t.Errorf("default refresh = %v", got)
	}
	if got := NewLoader(time.Second).RefreshInterval; got != MinStatusPollInterval {
		t.Errorf("floor refresh = %v", got)
	}
}

func TestModeLabels(t *testing.T) {
	for _, m := range []string{"aether", "aether-fm", "ardop", "ax25", "mercury", "vara-hf", "vara-fm"} {
		if _, ok := modemLabels[m]; !ok {
			t.Errorf("missing modem label %q", m)
		}
	}
	if got := catalog.FormatFrequency(7101000); got != "7.101 MHz" {
		t.Errorf("format = %q", got)
	}
}
