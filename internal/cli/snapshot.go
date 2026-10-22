package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/breichr/caskvet/internal/score"
)

// Snapshot is the persisted scan state of one run. It stores the full
// scan (including the signing authority per cask) so watch diffs need
// no restructuring later. Keep-all retention: nothing is ever deleted.
type Snapshot struct {
	Timestamp time.Time                  `json:"timestamp"`
	Casks     map[string]*score.CaskScan `json:"casks"`
}

// ErrNoSnapshot means no snapshot exists yet (first run).
var ErrNoSnapshot = errors.New("no snapshot yet")

// latestPointer is the latest.json content.
type latestPointer struct {
	Snapshot  string    `json:"snapshot"`
	Timestamp time.Time `json:"timestamp"`
}

// snapshotFileName derives the storage file name for a timestamp.
func snapshotFileName(ts time.Time) string {
	return "snapshot-" + ts.UTC().Format("20060102T150405Z") + ".json"
}

// saveSnapshot writes one JSON file per scan plus the latest.json
// pointer. Snapshots are versioned and diffable; retention is keep-all.
func saveSnapshot(dir string, snap *Snapshot) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating snapshot dir: %w", err)
	}
	name := snapshotFileName(snap.Timestamp)
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding snapshot: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
		return fmt.Errorf("writing snapshot: %w", err)
	}
	pointer, err := json.MarshalIndent(latestPointer{Snapshot: name, Timestamp: snap.Timestamp}, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding latest pointer: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "latest.json"), pointer, 0o600); err != nil {
		return fmt.Errorf("writing latest pointer: %w", err)
	}
	return nil
}

// loadLatestSnapshot reads the snapshot latest.json points at. A missing
// pointer or snapshot yields ErrNoSnapshot; a corrupt one yields
// ErrNoSnapshot too — an unreadable baseline must not crash a scan.
func loadLatestSnapshot(dir string) (*Snapshot, error) {
	pointerData, err := os.ReadFile(filepath.Join(dir, "latest.json"))
	if err != nil {
		return nil, ErrNoSnapshot
	}
	var pointer latestPointer
	if err := json.Unmarshal(pointerData, &pointer); err != nil {
		return nil, ErrNoSnapshot
	}
	data, err := os.ReadFile(filepath.Join(dir, pointer.Snapshot))
	if err != nil {
		return nil, ErrNoSnapshot
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, ErrNoSnapshot
	}
	if snap.Casks == nil {
		snap.Casks = map[string]*score.CaskScan{}
	}
	return &snap, nil
}
