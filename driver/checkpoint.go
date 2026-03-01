package driver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
)

const checkpointFileName = "checkpoint.json"

// checkpointData is the on-disk format for the checkpoint file.
// It stores the full prepared claims map so the driver can recover
// state after a container or pod restart without waiting for kubelet
// to re-call Prepare for each active claim.
type checkpointData struct {
	// Claims is keyed by claim UID string (not types.UID) for JSON
	// compatibility.
	Claims map[string]*PreparedClaim `json:"claims"`
}

// saveCheckpoint writes the current d.prepared map to the checkpoint
// file on disk. Must be called with d.mu held.
// Uses atomic write (temp + rename) to avoid partial reads.
func (d *Driver) saveCheckpoint() error {
	if d.checkpointPath == "" {
		return nil // checkpoint disabled
	}

	data := checkpointData{
		Claims: make(map[string]*PreparedClaim, len(d.prepared)),
	}
	for uid, pc := range d.prepared {
		data.Claims[string(uid)] = pc
	}

	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling checkpoint: %w", err)
	}

	dir := filepath.Dir(d.checkpointPath)
	tmp, err := os.CreateTemp(dir, ".checkpoint-*.json.tmp")
	if err != nil {
		return fmt.Errorf("creating temp checkpoint file: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("writing temp checkpoint: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("closing temp checkpoint: %w", err)
	}
	if err := os.Rename(tmpName, d.checkpointPath); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("renaming checkpoint: %w", err)
	}
	return nil
}

// LoadCheckpoint reads the checkpoint file and populates d.prepared.
// Must be called before any Prepare/Unprepare calls (at startup).
// If the file does not exist, d.prepared is left empty (fresh start).
// Returns the number of claims loaded.
func (d *Driver) LoadCheckpoint() (int, error) {
	if d.checkpointPath == "" {
		return 0, nil // checkpoint disabled
	}

	data, err := os.ReadFile(d.checkpointPath)
	if os.IsNotExist(err) {
		klog.InfoS("no checkpoint file found, starting fresh",
			"path", d.checkpointPath)
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("reading checkpoint file: %w", err)
	}

	var cp checkpointData
	if err := json.Unmarshal(data, &cp); err != nil {
		return 0, fmt.Errorf("unmarshaling checkpoint: %w", err)
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	for uid, pc := range cp.Claims {
		d.prepared[types.UID(uid)] = pc
	}
	return len(cp.Claims), nil
}
