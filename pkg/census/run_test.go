package census

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Use-Gauge/Gauge-core/pkg/horizon"
	"github.com/shopspring/decimal"
)

func TestRunRoundTripsAmountsAsStrings(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	// The exact string Horizon sent for a real pool, recorded 2026-09-06.
	const shares = "5494.2144063"
	const reserve = "241332637.7684097"

	pool := horizon.Pool{
		ID:              "abc",
		Type:            "constant_product",
		FeeBP:           30,
		TotalShares:     decimal.RequireFromString(shares),
		TotalTrustlines: 1,
		Reserves: [2]horizon.Reserve{
			{Asset: "native", Amount: decimal.RequireFromString("0.2318252")},
			{Asset: "GOLD:GDEU", Amount: decimal.RequireFromString(reserve)},
		},
	}
	if err := w.WritePools([]horizon.Pool{pool}); err != nil {
		t.Fatalf("WritePools: %v", err)
	}
	if err := w.Close(Manifest{Pools: 1, StartedAt: time.Now(), FinishedAt: time.Now()}); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The run file must carry amounts as quoted decimal strings. If they land
	// as JSON numbers, every consumer that reads them with a standard decoder
	// gets a float64 and the exactness is lost at the file boundary — outside
	// this package, where the no-float CI job cannot see it.
	line, err := os.ReadFile(filepath.Join(dir, "pools.jsonl"))
	if err != nil {
		t.Fatalf("read pools.jsonl: %v", err)
	}
	if !strings.Contains(string(line), `"total_shares":"`+shares+`"`) {
		t.Errorf("total_shares is not a quoted string in:\n%s", line)
	}
	if !strings.Contains(string(line), `"`+reserve+`"`) {
		t.Errorf("reserve amount is not a quoted string in:\n%s", line)
	}

	var back horizon.Pool
	if err := json.Unmarshal(line, &back); err != nil {
		t.Fatalf("unmarshal written pool: %v", err)
	}
	if back.TotalShares.String() != shares {
		t.Errorf("round trip: %q, want %q", back.TotalShares.String(), shares)
	}
}

// A run that did no attribution must not grow an empty positions.jsonl: an
// empty file and a pass that never ran are different facts.
func TestPositionsFileAbsentWhenNoAttribution(t *testing.T) {
	dir := t.TempDir()
	w, _ := NewWriter(dir)
	if err := w.Close(Manifest{}); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "positions.jsonl")); !os.IsNotExist(err) {
		t.Error("positions.jsonl exists for a run that attributed nothing")
	}
}

// Failures must survive into the manifest as an enumerated list. A count cannot
// be investigated, and null cannot be distinguished from "the pass never ran".
func TestManifestEnumeratesFailuresAndNeverEncodesNull(t *testing.T) {
	dir := t.TempDir()
	w, _ := NewWriter(dir)
	if err := w.Close(Manifest{Holders: &HolderStats{Gate: "test gate"}}); err != nil {
		t.Fatalf("Close: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if strings.Contains(string(b), `"pools_failed": null`) {
		t.Errorf("pools_failed encoded as null:\n%s", b)
	}
	if !strings.Contains(string(b), `"pools_failed": []`) {
		t.Errorf("pools_failed is not an explicit empty list:\n%s", b)
	}
	if !strings.Contains(string(b), "test gate") {
		t.Error("the attribution gate is not recorded in the manifest")
	}
}

// A run directory without a manifest is an interrupted run. Downstream readers
// depend on that distinction, so the manifest must be written last.
func TestManifestMarksACompletedRun(t *testing.T) {
	dir := t.TempDir()
	w, _ := NewWriter(dir)
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); !os.IsNotExist(err) {
		t.Fatal("manifest.json exists before Close")
	}
	if err := w.Close(Manifest{}); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
		t.Errorf("manifest.json missing after Close: %v", err)
	}
}
