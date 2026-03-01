// Package alloy provides Alloy configuration rendering, scaling
// computation, and HTTP client for the Alloy admin API.
package alloy

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/abhinavdahiya/k8s-dra-driver-trace-collector/driver/config"
	"github.com/abhinavdahiya/k8s-dra-driver-trace-collector/internal/atomicfile"
)

// ConfigParams holds the computed values for rendering a
// per-pod .alloy config file.
type ConfigParams struct {
	ClaimUID             string // raw claim UID
	Label                string // sanitized label (hyphens -> underscores)
	Shares               int64  // total shares for this claim
	SocketPath           string // /var/run/alloy/<claimUID>.sock
	MaxConcurrentStreams int    // scaled gRPC streams
	MaxRecvMsgSize       string // e.g. "4MiB"
	SpansPerSecond       int    // scaled spans/sec rate limit
	PipelineEntryPoint   string // admin's pipeline entry component
	DecisionWait         string // tail_sampling decision_wait (default "5s")
	NumTraces            int    // tail_sampling num_traces (default 50000)
}

// SocketPath returns the Unix domain socket path for a claim.
// This is a pure path computation with no side effects — it does not
// create the directory or socket file.
func SocketPath(socketDir, claimUID string) string {
	return filepath.Join(socketDir, claimUID, "claim_"+claimUID+".sock")
}

// ComputeParams calculates the scaled ConfigParams for a claim
// directly from the driver configuration.
func ComputeParams(claimUID string, shares int64, cfg *config.TraceDRADriverConfiguration) ConfigParams {
	units := int(shares) / cfg.Driver.StepSize

	sps := units * cfg.Scaling.SpansPerUnit
	if sps < cfg.Scaling.MinSpansPerSecond {
		sps = cfg.Scaling.MinSpansPerSecond
	}

	streams := units * cfg.Scaling.StreamsPerUnit
	if streams < 1 {
		streams = 1
	}

	// Alloy (River) identifiers must start with a letter. Kubernetes UIDs
	// are UUIDs that may start with a digit, so we prefix with "claim_".
	label := "claim_" + strings.ReplaceAll(claimUID, "-", "_")

	return ConfigParams{
		ClaimUID:             claimUID,
		Label:                label,
		Shares:               shares,
		SocketPath:           SocketPath(cfg.Alloy.SocketDir, claimUID),
		MaxConcurrentStreams: streams,
		MaxRecvMsgSize:       cfg.Scaling.MaxRecvMsgSize,
		SpansPerSecond:       sps,
		PipelineEntryPoint:   cfg.Alloy.PipelineEntryPoint,
		DecisionWait:         cfg.RateLimiting.DecisionWait.Duration.String(),
		NumTraces:            cfg.RateLimiting.NumTraces,
	}
}

// configTemplate is the text/template for per-pod .alloy config files.
var configTemplate = template.Must(template.New("alloy-config").Parse(`// Managed by trace-dra-driver. Do not edit.
// Claim: {{ .ClaimUID }}
// Shares: {{ .Shares }}

otelcol.receiver.otlp "{{ .Label }}" {
  grpc {
    transport              = "unix"
    endpoint               = "{{ .SocketPath }}"
    max_concurrent_streams = {{ .MaxConcurrentStreams }}
    max_recv_msg_size      = "{{ .MaxRecvMsgSize }}"
  }

  output {
    traces = [otelcol.processor.tail_sampling.{{ .Label }}.input]
  }
}

otelcol.processor.tail_sampling "{{ .Label }}" {
  decision_wait = "{{ .DecisionWait }}"
  num_traces    = {{ .NumTraces }}

  policy {
    name = "rate-limit"
    type = "rate_limiting"

    rate_limiting {
      spans_per_second = {{ .SpansPerSecond }}
    }
  }

  output {
    traces = [{{ .PipelineEntryPoint }}]
  }
}
`))

// RenderConfig renders the .alloy config file content for the given params.
func RenderConfig(params ConfigParams) ([]byte, error) {
	var buf bytes.Buffer
	if err := configTemplate.Execute(&buf, params); err != nil {
		return nil, fmt.Errorf("rendering alloy config template: %w", err)
	}
	return buf.Bytes(), nil
}

// ConfigFileName returns the config file name for a claim.
func ConfigFileName(claimUID string) string {
	return fmt.Sprintf("claim-%s.alloy", claimUID)
}

// WriteConfigFile writes the rendered config to
// <configDir>/claim-<claimUID>.alloy atomically (write to temp, then rename).
func WriteConfigFile(configDir string, claimUID string, content []byte) error {
	target := filepath.Join(configDir, ConfigFileName(claimUID))

	return atomicfile.WriteFile(target, content)
}

// DeleteConfigFile removes <configDir>/claim-<claimUID>.alloy.
// Returns nil if the file does not exist (idempotent).
func DeleteConfigFile(configDir string, claimUID string) error {
	target := filepath.Join(configDir, ConfigFileName(claimUID))
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("deleting config file %s: %w", target, err)
	}
	return nil
}

// SyncAdminConfig synchronizes *.alloy files from srcDir into dstDir.
// Only files whose content has changed (or is missing in dstDir) are
// written. Files in dstDir that exist in srcDir but have identical
// content are left untouched. Files in dstDir that do NOT exist in
// srcDir and do NOT have a "claim-" prefix are removed (stale admin
// files from a previous ConfigMap revision).
//
// Returns true if any files were written or removed (caller should
// reload Alloy), and any error encountered.
func SyncAdminConfig(srcDir, dstDir string) (changed bool, err error) {
	srcMatches, err := filepath.Glob(filepath.Join(srcDir, "*.alloy"))
	if err != nil {
		return false, fmt.Errorf("globbing admin config dir %s: %w", srcDir, err)
	}

	// Build set of source file basenames for orphan detection.
	srcNames := make(map[string]struct{}, len(srcMatches))
	for _, src := range srcMatches {
		srcNames[filepath.Base(src)] = struct{}{}
	}

	// Copy new or changed files from src to dst.
	for _, src := range srcMatches {
		baseName := filepath.Base(src)
		// Never overwrite reconciler-managed claim configs.
		if strings.HasPrefix(baseName, "claim-") {
			continue
		}
		srcData, err := os.ReadFile(src)
		if err != nil {
			return changed, fmt.Errorf("reading %s: %w", src, err)
		}
		dstPath := filepath.Join(dstDir, baseName)

		// Compare with existing file; skip if identical.
		dstData, readErr := os.ReadFile(dstPath)
		if readErr == nil && bytes.Equal(srcData, dstData) {
			continue
		}

		if err := atomicfile.WriteFile(dstPath, srcData); err != nil {
			return changed, err
		}
		changed = true
	}

	// Remove stale admin files: files in dstDir that are NOT in srcDir
	// and NOT claim-managed (claim-*.alloy) files.
	dstMatches, err := filepath.Glob(filepath.Join(dstDir, "*.alloy"))
	if err != nil {
		return changed, fmt.Errorf("globbing config dir %s: %w", dstDir, err)
	}
	for _, dst := range dstMatches {
		baseName := filepath.Base(dst)
		// Skip claim-managed files — those are the reconciler's business.
		if strings.HasPrefix(baseName, "claim-") {
			continue
		}
		// Skip files that exist in the source.
		if _, ok := srcNames[baseName]; ok {
			continue
		}
		// Stale admin file — remove it.
		if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
			return changed, fmt.Errorf("removing stale admin file %s: %w", dst, err)
		}
		changed = true
	}

	return changed, nil
}
