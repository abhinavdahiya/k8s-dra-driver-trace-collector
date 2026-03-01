// Package cdi provides CDI (Container Device Interface) spec
// generation for the trace DRA driver.
package cdi

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	// CDIVersion is the CDI spec version.
	CDIVersion = "0.6.0"

	// CDIKind is the CDI device kind.
	CDIKind = "trace.example.com/trace"
)

// Spec represents a CDI specification document.
type Spec struct {
	CDIVersion string   `json:"cdiVersion"`
	Kind       string   `json:"kind"`
	Devices    []Device `json:"devices"`
}

// Device represents a single CDI device entry.
type Device struct {
	Name           string         `json:"name"`
	ContainerEdits ContainerEdits `json:"containerEdits"`
}

// ContainerEdits describes the modifications to apply to a container.
type ContainerEdits struct {
	Env    []string `json:"env"`
	Mounts []Mount  `json:"mounts"`
}

// Mount describes a bind mount.
type Mount struct {
	HostPath      string   `json:"hostPath"`
	ContainerPath string   `json:"containerPath"`
	Type          string   `json:"type,omitempty"`
	Options       []string `json:"options,omitempty"`
}

// DeviceID returns the fully qualified CDI device ID for a claim.
func DeviceID(claimUID string) string {
	return fmt.Sprintf("trace.example.com/trace=%s", claimUID)
}

// specFileName returns the CDI spec file name for a claim.
func specFileName(claimUID string) string {
	return fmt.Sprintf("trace-%s.json", claimUID)
}

// NewSpec creates a CDI Spec for a claim with the given socket path.
// The mount exposes the socket's parent directory (e.g.
// /var/run/alloy/<claimUID>/) so the consumer can access the socket
// at the same absolute path.
func NewSpec(claimUID string, socketPath string) *Spec {
	socketDir := filepath.Dir(socketPath)
	return &Spec{
		CDIVersion: CDIVersion,
		Kind:       CDIKind,
		Devices: []Device{
			{
				Name: claimUID,
				ContainerEdits: ContainerEdits{
					Env: []string{
						fmt.Sprintf("TRACE_ENDPOINT=unix://%s", socketPath),
					},
					Mounts: []Mount{
						{
							HostPath:      socketDir,
							ContainerPath: socketDir,
							Options:       []string{"bind"},
						},
					},
				},
			},
		},
	}
}

// WriteSpec generates the CDI JSON and writes it to
// <cdiDir>/trace-<claimUID>.json.
func WriteSpec(cdiDir string, claimUID string, socketPath string) error {
	spec := NewSpec(claimUID, socketPath)

	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling CDI spec: %w", err)
	}

	target := filepath.Join(cdiDir, specFileName(claimUID))

	// Atomic write: temp file + rename.
	tmp, err := os.CreateTemp(cdiDir, ".trace-*.json.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("renaming temp file to %s: %w", target, err)
	}
	return nil
}

// DeleteSpec removes <cdiDir>/trace-<claimUID>.json.
// Returns nil if the file does not exist (idempotent).
func DeleteSpec(cdiDir string, claimUID string) error {
	target := filepath.Join(cdiDir, specFileName(claimUID))
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("deleting CDI spec %s: %w", target, err)
	}
	return nil
}
