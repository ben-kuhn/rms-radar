package catalog

import (
	"testing"

	"github.com/ben-kuhn/rms-radar/internal/cmsapi"
)

func TestModeFamily(t *testing.T) {
	cases := map[string]string{
		"VARA": "VARA", "VARA FM": "VARA FM", "VARA FM WIDE": "VARA FM",
		"Mercury": "Mercury", "VardeFM": "VardeFM",
		"Mercury 500": "Mercury", "VardeFM WIDE": "VardeFM",
		"MODEM73 2400": "MODEM73", "DATA2G 2750": "DATA2G",
		"IONHF": "IONHF", "XARA": "XARA", "Aether": "Aether", "ARDOP 2000": "ARDOP",
		"Packet 1200": "Packet", "Pactor 3": "PACTOR", "Robust Packet": "Robust Packet",
		"WINMOR 1600": "WINMOR", "Unknown": "Other",
	}
	for mode, want := range cases {
		if got := modeFamily(mode); got != want {
			t.Errorf("modeFamily(%q) = %q, want %q", mode, got, want)
		}
	}
}

func TestBandForFrequency(t *testing.T) {
	cases := map[float64]string{
		7.103e6:  "40m",
		14.1e6:   "20m",
		3.592e6:  "80m",
		10.147e6: "30m",
		145.03e6: "2m",
		440e6:    "70cm",
		50.1e6:   "6m",
		28.5e6:   "10m",
		1.5e6:    "HF",
		1000e6:   "Other",
	}
	for hz, want := range cases {
		if got := bandForFrequency(hz); got != want {
			t.Errorf("bandForFrequency(%v) = %q, want %q", hz, got, want)
		}
	}
}

func TestFormatFrequency(t *testing.T) {
	cases := map[float64]string{
		0:         "",
		7103000:   "7.103 MHz",
		7101300:   "7.1013 MHz",
		7042500:   "7.0425 MHz",
		145030000: "145.03 MHz",
		1500:      "1.5 kHz",
	}
	for hz, want := range cases {
		if got := formatFrequency(hz); got != want {
			t.Errorf("formatFrequency(%v) = %q, want %q", hz, got, want)
		}
	}
}

func TestNormalize(t *testing.T) {
	status := &cmsapi.GatewayStatus{Gateways: []cmsapi.Gateway{{
		Callsign:         "K0TEST-10",
		Latitude:         40,
		Longitude:        -100,
		HoursSinceStatus: 1,
		Channels: []cmsapi.GatewayChannel{{
			SupportedModes: "VARA",
			Frequency:      7103000,
			ServiceCode:    "mercury",
			Gridsquare:     "EM38",
			OperatingHours: "06-04",
		}},
	}}}

	stations := Normalize(status)
	if len(stations) != 1 {
		t.Fatalf("got %d stations", len(stations))
	}
	ch := stations[0].Channels[0]
	if ch.ServiceCode != "MERCURY" {
		t.Errorf("ServiceCode = %q, want MERCURY (uppercased)", ch.ServiceCode)
	}
	if ch.Mode != "Mercury" {
		t.Errorf("Mode = %q, want Mercury (service-code label)", ch.Mode)
	}
	if ch.RawMode != "VARA" {
		t.Errorf("RawMode = %q, want VARA (as reported)", ch.RawMode)
	}
	if ch.Band != "40m" {
		t.Errorf("Band = %q, want 40m", ch.Band)
	}
	if ch.OperatingHours != "06-04" {
		t.Errorf("OperatingHours = %q, want 06-04", ch.OperatingHours)
	}
}

func TestNormalizeHFUsesDialFrequency(t *testing.T) {
	cases := []struct {
		name       string
		hz         float64
		wantHz     float64
		wantString string
	}{
		{"HF center to dial", 7042500, 7041000, "7.041 MHz"},
		{"VHF unchanged", 145030000, 145030000, "145.03 MHz"},
		{"6m unchanged", 50125000, 50125000, "50.125 MHz"},
	}
	for _, c := range cases {
		ch := normalizeChannel(cmsapi.GatewayChannel{
			SupportedModes: "VARA",
			ServiceCode:    "PUBLIC",
			Frequency:      c.hz,
		})
		if ch.FrequencyHz != c.wantHz || ch.Frequency != c.wantString {
			t.Errorf("%s: frequency = %v (%q), want %v (%q)", c.name, ch.FrequencyHz, ch.Frequency, c.wantHz, c.wantString)
		}
		if ch.CenterFrequencyHz != c.hz {
			t.Errorf("%s: center frequency = %v, want %v", c.name, ch.CenterFrequencyHz, c.hz)
		}
	}
}

func TestCommunityModemLabels(t *testing.T) {
	cases := []struct{ sc, raw, want string }{
		{"MERCURY", "VARA", "Mercury"},
		{"MERCURY", "VARA 500", "Mercury 500"},
		{"VARDEFM", "VARA", "VardeFM"},
		{"VARDEFM", "VARA FM WIDE", "VardeFM WIDE"},
		{"MODEM73", "Packet 2400", "MODEM73"},
		{"DATA2G", "VARA 2750", "DATA2G"},
		{"DATA2G", "VARA 500", "DATA2G"},
		{"IONHF", "ARDOP 1000", "IONHF"},
		{"IONHF", "ARDOP", "IONHF"},
		{"IONHF", "VARA 500", "IONHF"},
		{"MODEM73", "ARDOP 2000", "MODEM73"},
		{"DATA2G", "Packet 1200", "DATA2G"},
		{"XARA", "VARA", "XARA"},
		{"XARA", "VARA 500", "XARA"},
		{"AETHER", "VARA", "Aether"},
		{"AETHER", "VARA HF", "Aether"},
		// Non-VARA modes under a community code keep their real mode.
		{"MERCURY", "ARDOP 2000", "ARDOP 2000"},
		{"VARDEFM", "Packet 1200", "Packet 1200"},
		// Access codes keep the reported mode.
		{"PUBLIC", "VARA 500", "VARA 500"},
	}
	for _, c := range cases {
		ch := normalizeChannel(cmsapi.GatewayChannel{SupportedModes: c.raw, ServiceCode: c.sc})
		if ch.Mode != c.want {
			t.Errorf("%s %q: mode = %q, want %q", c.sc, c.raw, ch.Mode, c.want)
		}
		if ch.RawMode != c.raw {
			t.Errorf("%s %q: raw_mode = %q, want %q", c.sc, c.raw, ch.RawMode, c.raw)
		}
	}
}
