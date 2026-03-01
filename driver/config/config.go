// Package config defines the TraceDRADriverConfiguration type and
// provides loading, defaulting, and validation for the driver's
// component-config YAML file.
package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

const (
	// APIVersion is the expected apiVersion in the config file.
	APIVersion = "trace.example.com/v1alpha1"

	// Kind is the expected kind in the config file.
	Kind = "TraceDRADriverConfiguration"
)

// TraceDRADriverConfiguration configures the trace DRA driver.
//
//	apiVersion: trace.example.com/v1alpha1
//	kind: TraceDRADriverConfiguration
type TraceDRADriverConfiguration struct {
	metav1.TypeMeta `json:",inline" yaml:",inline"`

	// Driver identity and capacity settings.
	Driver DriverSpec `json:"driver" yaml:"driver"`

	// Alloy collector integration settings.
	Alloy AlloySpec `json:"alloy" yaml:"alloy"`

	// Per-unit scaling parameters for share-to-resource conversion.
	Scaling ScalingSpec `json:"scaling" yaml:"scaling"`

	// Per-pod rate limiting settings (implemented via tail_sampling
	// rate_limiting policy).
	RateLimiting RateLimitingSpec `json:"rateLimiting" yaml:"rateLimiting"`

	// Reconciler loop settings.
	Reconciler ReconcilerSpec `json:"reconciler" yaml:"reconciler"`
}

// DriverSpec configures the DRA driver identity and capacity.
type DriverSpec struct {
	// Name is the DRA driver name registered with kubelet.
	// Default: "trace.example.com"
	Name string `json:"name" yaml:"name"`

	// TotalShares is the per-node trace capacity expressed as
	// consumable shares in the ResourceSlice.
	// Default: 1000
	TotalShares int `json:"totalShares" yaml:"totalShares"`

	// StepSize is the number of shares per unit, matching
	// requestPolicy.step in the ResourceSlice.
	// Default: 10
	StepSize int `json:"stepSize" yaml:"stepSize"`

	// CheckpointDir is the directory for the checkpoint file
	// (checkpoint.json). Should be a hostPath volume so it
	// survives container and pod restarts.
	// Default: "/var/lib/trace-dra-driver"
	CheckpointDir string `json:"checkpointDir" yaml:"checkpointDir"`
}

// AlloySpec configures the Alloy collector integration.
type AlloySpec struct {
	// Address is the Alloy admin HTTP API address for reload calls.
	// Default: "http://127.0.0.1:12345"
	Address string `json:"address" yaml:"address"`

	// ConfigDir is the writable directory for per-pod .alloy config
	// files, shared with the Alloy container via emptyDir.
	// Default: "/etc/alloy/config"
	ConfigDir string `json:"configDir" yaml:"configDir"`

	// AdminConfigDir is the read-only directory with admin base config
	// (e.g. ConfigMap mount). If set, the driver copies *.alloy files
	// from here into ConfigDir on startup.
	// Default: "" (disabled)
	AdminConfigDir string `json:"adminConfigDir,omitempty" yaml:"adminConfigDir,omitempty"`

	// SocketDir is the directory for per-pod Unix domain sockets.
	// Default: "/var/run/alloy"
	SocketDir string `json:"socketDir" yaml:"socketDir"`

	// PipelineEntryPoint is the Alloy component reference that per-pod
	// processors forward traces to (the admin's pipeline entry).
	// Required.
	PipelineEntryPoint string `json:"pipelineEntryPoint" yaml:"pipelineEntryPoint"`
}

// ScalingSpec configures per-unit scaling parameters.
type ScalingSpec struct {
	// SpansPerUnit is the spans/sec rate limit per unit
	// (1 unit = driver.stepSize shares).
	// Default: 100
	SpansPerUnit int `json:"spansPerUnit" yaml:"spansPerUnit"`

	// MinSpansPerSecond is the floor for the spans/sec rate limit.
	// Guarantees minimum throughput even for a 1-unit claim.
	// Default: 100
	MinSpansPerSecond int `json:"minSpansPerSecond" yaml:"minSpansPerSecond"`

	// StreamsPerUnit is the gRPC max_concurrent_streams per unit.
	// Default: 10
	StreamsPerUnit int `json:"streamsPerUnit" yaml:"streamsPerUnit"`

	// MaxRecvMsgSize is the gRPC max receive message size
	// (fixed, not scaled per unit).
	// Default: "4MiB"
	MaxRecvMsgSize string `json:"maxRecvMsgSize" yaml:"maxRecvMsgSize"`
}

// RateLimitingSpec configures per-pod rate limiting (implemented via
// tail_sampling rate_limiting policy).
type RateLimitingSpec struct {
	// DecisionWait is how long the rate limiter buffers spans for a
	// trace before making a sampling decision. Maps to
	// tail_sampling.decision_wait in the generated Alloy config.
	// Default: "5s"
	DecisionWait metav1.Duration `json:"decisionWait" yaml:"decisionWait"`

	// NumTraces is the maximum number of traces held in the rate
	// limiter's buffer simultaneously. Maps to
	// tail_sampling.num_traces in the generated Alloy config.
	// Default: 50000
	NumTraces int `json:"numTraces" yaml:"numTraces"`
}

// ReconcilerSpec configures the reconciler loop.
type ReconcilerSpec struct {
	// Interval is the periodic safety-net timer for the reconciler.
	// Default: "30s"
	Interval metav1.Duration `json:"interval" yaml:"interval"`
}

// LoadConfigFile reads a TraceDRADriverConfiguration from the given
// YAML file path, applies defaults, and validates.
func LoadConfigFile(path string) (*TraceDRADriverConfiguration, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	cfg := &TraceDRADriverConfiguration{}
	if err := yaml.UnmarshalStrict(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	SetDefaults(cfg)

	if err := Validate(cfg); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return cfg, nil
}

// SetDefaults fills zero-value fields with their documented defaults.
func SetDefaults(cfg *TraceDRADriverConfiguration) {
	if cfg.Driver.Name == "" {
		cfg.Driver.Name = "trace.example.com"
	}
	if cfg.Driver.TotalShares == 0 {
		cfg.Driver.TotalShares = 1000
	}
	if cfg.Driver.StepSize == 0 {
		cfg.Driver.StepSize = 10
	}
	if cfg.Driver.CheckpointDir == "" {
		cfg.Driver.CheckpointDir = "/var/lib/trace-dra-driver"
	}
	if cfg.Alloy.Address == "" {
		cfg.Alloy.Address = "http://127.0.0.1:12345"
	}
	if cfg.Alloy.ConfigDir == "" {
		cfg.Alloy.ConfigDir = "/etc/alloy/config"
	}
	if cfg.Alloy.SocketDir == "" {
		cfg.Alloy.SocketDir = "/var/run/alloy"
	}
	if cfg.Scaling.SpansPerUnit == 0 {
		cfg.Scaling.SpansPerUnit = 100
	}
	if cfg.Scaling.MinSpansPerSecond == 0 {
		cfg.Scaling.MinSpansPerSecond = 100
	}
	if cfg.Scaling.StreamsPerUnit == 0 {
		cfg.Scaling.StreamsPerUnit = 10
	}
	if cfg.Scaling.MaxRecvMsgSize == "" {
		cfg.Scaling.MaxRecvMsgSize = "4MiB"
	}
	if cfg.RateLimiting.DecisionWait.Duration == 0 {
		cfg.RateLimiting.DecisionWait = metav1.Duration{Duration: 5 * time.Second}
	}
	if cfg.RateLimiting.NumTraces == 0 {
		cfg.RateLimiting.NumTraces = 50000
	}
	if cfg.Reconciler.Interval.Duration == 0 {
		cfg.Reconciler.Interval = metav1.Duration{Duration: 30 * time.Second}
	}
}

// Validate checks that the configuration is valid and returns an
// aggregate error describing all problems found.
func Validate(cfg *TraceDRADriverConfiguration) error {
	var errs []string

	if cfg.APIVersion != APIVersion {
		errs = append(errs, fmt.Sprintf("apiVersion must be %q, got %q", APIVersion, cfg.APIVersion))
	}
	if cfg.Kind != Kind {
		errs = append(errs, fmt.Sprintf("kind must be %q, got %q", Kind, cfg.Kind))
	}
	if cfg.Alloy.PipelineEntryPoint == "" {
		errs = append(errs, "alloy.pipelineEntryPoint is required")
	}
	if cfg.Driver.TotalShares <= 0 {
		errs = append(errs, "driver.totalShares must be positive")
	}
	if cfg.Driver.StepSize <= 0 {
		errs = append(errs, "driver.stepSize must be positive")
	}
	if cfg.Scaling.SpansPerUnit <= 0 {
		errs = append(errs, "scaling.spansPerUnit must be positive")
	}
	if cfg.RateLimiting.DecisionWait.Duration < 0 {
		errs = append(errs, "rateLimiting.decisionWait must not be negative")
	}
	if cfg.Reconciler.Interval.Duration < 0 {
		errs = append(errs, "reconciler.interval must not be negative")
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}
