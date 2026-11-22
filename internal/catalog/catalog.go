package catalog

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ben-kuhn/rms-radar/internal/cmsapi"
)

// CommunityModems maps Winlink service codes that name a community modem to the
// label shown in the mode column. The API advertises these channels under the
// modem's underlying protocol (for example VARA), so the service code is the
// reliable identity. Matching the advertised mode string would be fragile.
// Codes not listed here (PUBLIC, private groups) keep the reported mode.
var CommunityModems = map[string]string{
	"MERCURY": "Mercury",
	"VARDEFM": "VardeFM",
	"MODEM73": "MODEM73",
	"DATA2G":  "DATA2G",
	"IONHF":   "IONHF",
	"XARA":    "XARA",
}

// dedicatedModemCodes are service codes whose whole purpose is the modem
// itself, so the label replaces the protocol name even when it is not VARA
// (for example IONHF's "ARDOP 1000" becomes "IONHF"). Their reported bandwidth
// is deliberately omitted because it varies by channel and modem configuration:
// DATA2G uses 500/2400 Hz and MODEM73 uses 600/2400 Hz.
var dedicatedModemCodes = map[string]bool{
	"MODEM73": true,
	"DATA2G":  true,
	"IONHF":   true,
	"XARA":    true,
}

// Channel is a normalized, display-ready channel. Mode is the label to show;
// for a community-modem service code that is the service's name (Mercury,
// VardeFM, MODEM73, DATA2G, IONHF, XARA), otherwise it is exactly what the Winlink API
// reports. RawMode keeps the API value either way.
type Channel struct {
	ServiceCode       string  `json:"service_code"`
	Mode              string  `json:"mode"`                // display label
	RawMode           string  `json:"raw_mode"`            // as reported by Winlink
	ModeFamily        string  `json:"mode_family"`         // VARA, VARA FM, Mercury, ...
	FrequencyHz       float64 `json:"frequency_hz"`        // radio dial frequency
	Frequency         string  `json:"frequency"`           // human-readable dial frequency
	CenterFrequencyHz float64 `json:"center_frequency_hz"` // Winlink USB center
	CenterFrequency   string  `json:"center_frequency"`    // human-readable center frequency
	Gridsquare        string  `json:"gridsquare"`
	OperatingHours    string  `json:"operating_hours"`
	Baud              string  `json:"baud,omitempty"`
	RadioRange        string  `json:"radio_range,omitempty"`
	Antenna           string  `json:"antenna,omitempty"`
	Band              string  `json:"band"`
}

// Station is a gateway with its channels flattened and enriched.
type Station struct {
	Callsign         string    `json:"callsign"`
	BaseCallsign     string    `json:"base_callsign"`
	Latitude         float64   `json:"latitude"`
	Longitude        float64   `json:"longitude"`
	HoursSinceStatus int       `json:"hours_since_status"`
	LastStatus       string    `json:"last_status"`
	Comments         string    `json:"comments"`
	Channels         []Channel `json:"channels"`
}

// Normalize converts the raw feed into a flat list of stations, sorted by
// callsign, preserving the mode and service code exactly as reported.
func Normalize(status *cmsapi.GatewayStatus) []Station {
	if status == nil {
		return nil
	}
	stations := make([]Station, 0, len(status.Gateways))
	for _, gw := range status.Gateways {
		st := Station{
			Callsign:         gw.Callsign,
			BaseCallsign:     gw.BaseCallsign,
			Latitude:         gw.Latitude,
			Longitude:        gw.Longitude,
			HoursSinceStatus: gw.HoursSinceStatus,
			LastStatus:       gw.LastStatus,
			Comments:         gw.Comments,
			Channels:         make([]Channel, 0, len(gw.Channels)),
		}
		for _, ch := range gw.Channels {
			st.Channels = append(st.Channels, normalizeChannel(ch))
		}
		stations = append(stations, st)
	}
	sort.Slice(stations, func(i, j int) bool {
		return stations[i].Callsign < stations[j].Callsign
	})
	return stations
}

// Winlink reports HF channel frequencies as the center of the 3 kHz USB
// passband. Radio dials (and PAT's gateway list) use the suppressed-carrier
// frequency 1.5 kHz below that center. VHF/UHF FM channels are not offset.
const hfDialOffsetHz = 1500

func normalizeChannel(ch cmsapi.GatewayChannel) Channel {
	raw := strings.TrimSpace(ch.SupportedModes)
	if raw == "" {
		raw = "Unknown"
	}
	sc := strings.ToUpper(strings.TrimSpace(ch.ServiceCode))
	mode := communityMode(sc, raw)
	dialHz := ch.Frequency
	if dialHz > 0 && dialHz < 30e6 {
		dialHz -= hfDialOffsetHz
	}
	return Channel{
		ServiceCode:       sc,
		Mode:              mode,
		RawMode:           raw,
		ModeFamily:        modeFamily(mode),
		FrequencyHz:       dialHz,
		Frequency:         formatFrequency(dialHz),
		CenterFrequencyHz: ch.Frequency,
		CenterFrequency:   formatFrequency(ch.Frequency),
		Gridsquare:        ch.Gridsquare,
		OperatingHours:    ch.OperatingHours,
		Baud:              ch.Baud,
		RadioRange:        ch.RadioRange,
		Antenna:           ch.Antenna,
		Band:              bandForFrequency(dialHz),
	}
}

// communityMode returns the display label for a channel. A community-modem
// service code replaces the VARA family name (keeping any bandwidth suffix, so
// "VARA 500" becomes "Mercury 500"), while every other mode is left exactly as
// reported. This matters when a gateway publishes non-VARA channels under a
// community code (for example an ARDOP channel under MERCURY).
//
// Dedicated project codes (IONHF, DATA2G, MODEM73) always become just the
// service code. Their advertised protocol/bandwidth is not a stable identity:
// IONHF reports ARDOP, DATA2G reports VARA (500 or 2400), and MODEM73 reports
// Packet 2400 or 600. The service code is authoritative.
func communityMode(serviceCode, mode string) string {
	label, ok := CommunityModems[serviceCode]
	if !ok {
		return mode
	}
	fields := strings.Fields(mode)
	if len(fields) == 0 {
		return mode
	}
	if dedicatedModemCodes[serviceCode] {
		return label
	}
	if !strings.EqualFold(fields[0], "VARA") {
		// MERCURY/VARDEFM are also used for ordinary (non-VARA) channels;
		// leave those modes alone.
		return mode
	}
	rest := fields[1:]
	if len(rest) > 0 && strings.EqualFold(rest[0], "FM") {
		rest = rest[1:]
	}
	if len(rest) == 0 {
		return label
	}
	return label + " " + strings.Join(rest, " ")
}

func modeFamily(mode string) string {
	m := strings.ToUpper(mode)
	switch {
	case strings.HasPrefix(m, "VARA FM"):
		return "VARA FM"
	case strings.HasPrefix(m, "VARA"):
		return "VARA"
	case strings.HasPrefix(m, "MERCURY"):
		return "Mercury"
	case strings.HasPrefix(m, "VARDEFM"):
		return "VardeFM"
	case strings.HasPrefix(m, "MODEM73"):
		return "MODEM73"
	case strings.HasPrefix(m, "DATA2G"):
		return "DATA2G"
	case strings.HasPrefix(m, "IONHF"):
		return "IONHF"
	case strings.HasPrefix(m, "XARA"):
		return "XARA"
	case strings.HasPrefix(m, "ARDOP"):
		return "ARDOP"
	case strings.HasPrefix(m, "PACKET"):
		return "Packet"
	case strings.HasPrefix(m, "PACTOR"):
		return "PACTOR"
	case strings.HasPrefix(m, "ROBUST"):
		return "Robust Packet"
	case strings.HasPrefix(m, "WINMOR"):
		return "WINMOR"
	default:
		return "Other"
	}
}

func formatFrequency(hz float64) string {
	if hz <= 0 {
		return ""
	}
	switch {
	case hz >= 1e9:
		return trimZeros(fmt.Sprintf("%.4f", hz/1e9)) + " GHz"
	case hz >= 1e6:
		// Four decimal places give 100 Hz resolution; three would round
		// 7.1013 MHz to 7.101 and make the dial frequency unusable.
		return trimZeros(fmt.Sprintf("%.4f", hz/1e6)) + " MHz"
	case hz >= 1e3:
		return trimZeros(fmt.Sprintf("%.3f", hz/1e3)) + " kHz"
	default:
		return fmt.Sprintf("%.0f Hz", hz)
	}
}

func trimZeros(s string) string {
	s = strings.TrimRight(s, "0")
	return strings.TrimRight(s, ".")
}

// bandForFrequency maps a frequency in Hz to an amateur band name.
func bandForFrequency(hz float64) string {
	mhz := hz / 1e6
	switch {
	case mhz >= 1.8 && mhz <= 2.0:
		return "160m"
	case mhz >= 3.5 && mhz <= 4.0:
		return "80m"
	case mhz >= 5.330 && mhz <= 5.405:
		return "60m"
	case mhz >= 7.0 && mhz <= 7.3:
		return "40m"
	case mhz >= 10.1 && mhz <= 10.15:
		return "30m"
	case mhz >= 14.0 && mhz <= 14.35:
		return "20m"
	case mhz >= 18.068 && mhz <= 18.168:
		return "17m"
	case mhz >= 21.0 && mhz <= 21.45:
		return "15m"
	case mhz >= 24.89 && mhz <= 24.99:
		return "12m"
	case mhz >= 28.0 && mhz <= 29.7:
		return "10m"
	case mhz >= 50 && mhz <= 54:
		return "6m"
	case mhz >= 144 && mhz <= 148:
		return "2m"
	case mhz >= 222 && mhz <= 225:
		return "1.25m"
	case mhz >= 420 && mhz <= 450:
		return "70cm"
	case mhz >= 902 && mhz <= 928:
		return "33cm"
	case mhz >= 1240 && mhz <= 1300:
		return "23cm"
	default:
		if mhz < 30 {
			return "HF"
		}
		return "Other"
	}
}
