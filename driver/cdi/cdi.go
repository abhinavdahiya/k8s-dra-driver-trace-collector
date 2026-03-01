// Package cdi provides CDI (Container Device Interface) spec
// generation for the trace DRA driver.
package cdi

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/abhinavdahiya/k8s-dra-driver-trace-collector/internal/atomicfile"

	specs "tags.cncf.io/container-device-interface/specs-go"
)

const (
	// CDIKind is the CDI device kind.
	CDIKind = "trace.example.com/trace"
)

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
func NewSpec(claimUID string, socketPath string) *specs.Spec {
	socketDir := filepath.Dir(socketPath)
	return &specs.Spec{
		Version: specs.CurrentVersion,
		Kind:    CDIKind,
		Devices: []specs.Device{
			{
				Name: claimUID,
				ContainerEdits: specs.ContainerEdits{
					Env: []string{
						fmt.Sprintf("TRACE_ENDPOINT=unix://%s", socketPath),
					},
					Mounts: []*specs.Mount{
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

	return atomicfile.WriteFile(target, data)
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
