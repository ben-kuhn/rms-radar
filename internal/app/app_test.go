package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ben-kuhn/rms-radar/internal/cmsapi"
)

func TestStartRejectsInvalidRefresh(t *testing.T) {
	if _, err := Start(context.Background(), Config{Refresh: 0, Listen: "127.0.0.1:0"}); err == nil {
		t.Fatal("expected an error for a zero refresh interval")
	}
	if _, err := Start(context.Background(), Config{Refresh: -1, Listen: "127.0.0.1:0"}); err == nil {
		t.Fatal("expected an error for a negative refresh interval")
	}
}

func TestStartRejectsInvalidServiceCodes(t *testing.T) {
	_, err := Start(context.Background(), Config{
		Refresh:      time.Hour,
		Listen:       "127.0.0.1:0",
		ServiceCodes: []string{"../../etc/passwd"},
		WebFS:        fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}},
	})
	if err == nil {
		t.Fatal("expected an error for an invalid service code")
	}
}

func TestServiceCodesFromDiskValidates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service_codes.json")
	fallback := []string{"PUBLIC"}
	if got := serviceCodesFromDisk(path, fallback); len(got) != 1 || got[0] != "PUBLIC" {
		t.Fatalf("missing file = %v", got)
	}
	if err := os.WriteFile(path, []byte(`["../pwned"]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := serviceCodesFromDisk(path, fallback); len(got) != 1 || got[0] != "PUBLIC" {
		t.Fatalf("invalid file = %v, want fallback", got)
	}
	if err := os.WriteFile(path, []byte(`[" mercury ","PUBLIC"]`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := serviceCodesFromDisk(path, fallback)
	if len(got) != 2 || got[0] != "MERCURY" || got[1] != "PUBLIC" {
		t.Fatalf("valid file = %v", got)
	}
}

func TestStartAndClose(t *testing.T) {
	dir := t.TempDir()
	a, err := Start(context.Background(), Config{
		Version:      "test",
		Refresh:      time.Hour,
		Listen:       "127.0.0.1:0",
		CacheDir:     dir,
		ServiceCodes: []string{"PUBLIC"},
		WebFS:        fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(a.URL(), "http://127.0.0.1:") {
		t.Fatalf("URL = %q", a.URL())
	}
	a.Close()
}

func TestServiceCodesFromDiskMigratesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service_codes.json")
	fallback := append([]string(nil), cmsapi.DefaultServiceCodes...)
	seedMarker := path + ".seed"
	legacyMarker := path + ".legacy-v1"

	if err := os.WriteFile(path, []byte(`["PUBLIC","EMCOMM"]`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := serviceCodesFromDisk(path, fallback)
	if !sameServiceCodes(got, fallback) {
		t.Fatalf("legacy two-code default was not migrated: %v", got)
	}
	if _, err := os.Stat(seedMarker); err != nil {
		t.Fatalf("seed marker missing: %v", err)
	}

	// After the marker exists, an explicit choice of the old set is respected.
	if err := os.WriteFile(path, []byte(`["MERCURY","PUBLIC"]`), 0o644); err != nil {
		t.Fatal(err)
	}
	got = serviceCodesFromDisk(path, fallback)
	if len(got) != 2 || got[0] != "MERCURY" || got[1] != "PUBLIC" {
		t.Fatalf("explicit choice was overwritten: %v", got)
	}

	// A fresh 0.2.6 old-six default migrates to the 0.2.7 set.
	if err := os.Remove(seedMarker); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(legacyMarker); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`["DATA2G","EMCOMM","MERCURY","MODEM73","PUBLIC","VARDEFM"]`), 0o644); err != nil {
		t.Fatal(err)
	}
	got = serviceCodesFromDisk(path, fallback)
	if !sameServiceCodes(got, fallback) {
		t.Fatalf("old six-code default was not migrated: %v", got)
	}
	if _, ok := indexOf(got, "IONHF"); !ok {
		t.Fatalf("IONHF missing after migration: %v", got)
	}
	if _, ok := indexOf(got, "EMCOMM"); ok {
		t.Fatalf("EMCOMM should not be in the default set: %v", got)
	}
	if _, ok := indexOf(got, "XARA"); !ok {
		t.Fatalf("XARA missing from the default set: %v", got)
	}
	if _, ok := indexOf(got, "AETHER"); !ok {
		t.Fatalf("AETHER missing from the default set: %v", got)
	}

	// The seven-code default briefly shipped with EMCOMM in 1.0.x. It must
	// migrate to the current six-code default rather than persisting EMCOMM.
	if err := os.Remove(seedMarker); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(legacyMarker); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`["DATA2G","EMCOMM","IONHF","MERCURY","MODEM73","PUBLIC","VARDEFM"]`), 0o644); err != nil {
		t.Fatal(err)
	}
	got = serviceCodesFromDisk(path, fallback)
	if !sameServiceCodes(got, fallback) {
		t.Fatalf("seven-code default with EMCOMM was not migrated: %v", got)
	}
	if _, ok := indexOf(got, "EMCOMM"); ok {
		t.Fatalf("EMCOMM survived migration: %v", got)
	}

	// CarKit was observed with EMCOMM/MERCURY/PUBLIC and an existing seed marker
	// from 0.2.8; the legacy migration must still reach it.
	carKitPath := filepath.Join(t.TempDir(), "service_codes.json")
	if err := os.WriteFile(carKitPath, []byte(`["PUBLIC","EMCOMM","MERCURY"]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(carKitPath+".seed", []byte(serviceCodesSeedHash(fallback)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got = serviceCodesFromDisk(carKitPath, fallback)
	if !sameServiceCodes(got, fallback) {
		t.Fatalf("CarKit legacy list was not migrated: %v", got)
	}
	if _, ok := indexOf(got, "IONHF"); !ok {
		t.Fatalf("CarKit missing IONHF: %v", got)
	}

	// A custom selection is preserved on first use of the marker.
	_ = os.Remove(seedMarker)
	_ = os.Remove(legacyMarker)
	if err := os.WriteFile(path, []byte(`["MYGROUP","PUBLIC"]`), 0o644); err != nil {
		t.Fatal(err)
	}
	got = serviceCodesFromDisk(path, fallback)
	if len(got) != 2 || got[0] != "MYGROUP" || got[1] != "PUBLIC" {
		t.Fatalf("custom selection was overwritten: %v", got)
	}

	// A changed operator seed is authoritative.
	if err := os.WriteFile(path, []byte(`["MYGROUP","PUBLIC"]`), 0o644); err != nil {
		t.Fatal(err)
	}
	changed := []string{"IONHF", "PUBLIC"}
	got = serviceCodesFromDisk(path, changed)
	if !sameServiceCodes(got, changed) {
		t.Fatalf("changed seed was not adopted: %v", got)
	}
}

func indexOf(xs []string, want string) (int, bool) {
	for i, x := range xs {
		if x == want {
			return i, true
		}
	}
	return -1, false
}
