package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSetDefaults(t *testing.T) {
	cfg := &TraceDRADriverConfiguration{}
	SetDefaults(cfg)

	assert.Equal(t, "trace.example.com", cfg.Driver.Name)
	assert.Equal(t, 1000, cfg.Driver.TotalShares)
	assert.Equal(t, 10, cfg.Driver.StepSize)
	assert.Equal(t, "http://127.0.0.1:12345", cfg.Alloy.Address)
	assert.Equal(t, "/etc/alloy/config", cfg.Alloy.ConfigDir)
	assert.Equal(t, "/var/run/alloy", cfg.Alloy.SocketDir)
	assert.Equal(t, "", cfg.Alloy.AdminConfigDir)
	assert.Equal(t, 10240, cfg.Scaling.BytesPerUnit)
	assert.Equal(t, 10240, cfg.Scaling.MinBytesPerSecond)
	assert.Equal(t, 10, cfg.Scaling.StreamsPerUnit)
	assert.Equal(t, "4MiB", cfg.Scaling.MaxRecvMsgSize)
	assert.Equal(t, 5*time.Second, cfg.RateLimiting.DecisionWait.Duration)
	assert.Equal(t, 50000, cfg.RateLimiting.NumTraces)
	assert.Equal(t, 30*time.Second, cfg.Reconciler.Interval.Duration)
}

func TestSetDefaults_PreservesExplicitValues(t *testing.T) {
	cfg := &TraceDRADriverConfiguration{
		Driver: DriverSpec{
			Name:        "custom.driver",
			TotalShares: 500,
			StepSize:    5,
		},
		Alloy: AlloySpec{
			Address:            "http://alloy:9090",
			ConfigDir:          "/custom/config",
			SocketDir:          "/custom/sockets",
			PipelineEntryPoint: "otelcol.exporter.otlp.default.input",
		},
		Scaling: ScalingSpec{
			BytesPerUnit:      20480,
			MinBytesPerSecond: 5120,
			StreamsPerUnit:    20,
			MaxRecvMsgSize:    "8MiB",
		},
		RateLimiting: RateLimitingSpec{
			DecisionWait: metav1.Duration{Duration: 10 * time.Second},
			NumTraces:    100000,
		},
		Reconciler: ReconcilerSpec{
			Interval: metav1.Duration{Duration: 60 * time.Second},
		},
	}
	SetDefaults(cfg)

	assert.Equal(t, "custom.driver", cfg.Driver.Name)
	assert.Equal(t, 500, cfg.Driver.TotalShares)
	assert.Equal(t, 5, cfg.Driver.StepSize)
	assert.Equal(t, "http://alloy:9090", cfg.Alloy.Address)
	assert.Equal(t, "/custom/config", cfg.Alloy.ConfigDir)
	assert.Equal(t, "/custom/sockets", cfg.Alloy.SocketDir)
	assert.Equal(t, 20480, cfg.Scaling.BytesPerUnit)
	assert.Equal(t, 5120, cfg.Scaling.MinBytesPerSecond)
	assert.Equal(t, 20, cfg.Scaling.StreamsPerUnit)
	assert.Equal(t, "8MiB", cfg.Scaling.MaxRecvMsgSize)
	assert.Equal(t, 10*time.Second, cfg.RateLimiting.DecisionWait.Duration)
	assert.Equal(t, 100000, cfg.RateLimiting.NumTraces)
	assert.Equal(t, 60*time.Second, cfg.Reconciler.Interval.Duration)
}

func TestValidate_Valid(t *testing.T) {
	cfg := &TraceDRADriverConfiguration{
		TypeMeta: metav1.TypeMeta{
			APIVersion: APIVersion,
			Kind:       Kind,
		},
		Driver: DriverSpec{
			TotalShares: 1000,
			StepSize:    10,
		},
		Alloy: AlloySpec{
			PipelineEntryPoint: "otelcol.exporter.otlp.default.input",
		},
		Scaling: ScalingSpec{
			BytesPerUnit: 10240,
		},
		RateLimiting: RateLimitingSpec{
			DecisionWait: metav1.Duration{Duration: 5 * time.Second},
		},
		Reconciler: ReconcilerSpec{
			Interval: metav1.Duration{Duration: 30 * time.Second},
		},
	}
	assert.NoError(t, Validate(cfg))
}

func TestValidate_Errors(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*TraceDRADriverConfiguration)
		wantErr string
	}{
		{
			name: "wrong apiVersion",
			mutate: func(cfg *TraceDRADriverConfiguration) {
				cfg.APIVersion = "wrong/v1"
			},
			wantErr: "apiVersion must be",
		},
		{
			name: "wrong kind",
			mutate: func(cfg *TraceDRADriverConfiguration) {
				cfg.Kind = "WrongKind"
			},
			wantErr: "kind must be",
		},
		{
			name: "missing pipelineEntryPoint",
			mutate: func(cfg *TraceDRADriverConfiguration) {
				cfg.Alloy.PipelineEntryPoint = ""
			},
			wantErr: "alloy.pipelineEntryPoint is required",
		},
		{
			name: "zero totalShares",
			mutate: func(cfg *TraceDRADriverConfiguration) {
				cfg.Driver.TotalShares = 0
			},
			wantErr: "driver.totalShares must be positive",
		},
		{
			name: "negative stepSize",
			mutate: func(cfg *TraceDRADriverConfiguration) {
				cfg.Driver.StepSize = -1
			},
			wantErr: "driver.stepSize must be positive",
		},
		{
			name: "zero bytesPerUnit",
			mutate: func(cfg *TraceDRADriverConfiguration) {
				cfg.Scaling.BytesPerUnit = 0
			},
			wantErr: "scaling.bytesPerUnit must be positive",
		},
		{
			name: "negative decisionWait",
			mutate: func(cfg *TraceDRADriverConfiguration) {
				cfg.RateLimiting.DecisionWait = metav1.Duration{Duration: -1 * time.Second}
			},
			wantErr: "rateLimiting.decisionWait must not be negative",
		},
		{
			name: "negative reconciler interval",
			mutate: func(cfg *TraceDRADriverConfiguration) {
				cfg.Reconciler.Interval = metav1.Duration{Duration: -1 * time.Second}
			},
			wantErr: "reconciler.interval must not be negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(cfg)
			err := Validate(cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestValidate_MultipleErrors(t *testing.T) {
	cfg := &TraceDRADriverConfiguration{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "wrong",
			Kind:       "wrong",
		},
	}
	SetDefaults(cfg)
	err := Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "apiVersion must be")
	assert.Contains(t, err.Error(), "kind must be")
	assert.Contains(t, err.Error(), "alloy.pipelineEntryPoint is required")
}

func TestLoadConfigFile_Minimal(t *testing.T) {
	content := `apiVersion: trace.example.com/v1alpha1
kind: TraceDRADriverConfiguration
alloy:
  pipelineEntryPoint: otelcol.exporter.otlp.default.input
`
	path := writeTemp(t, content)
	cfg, err := LoadConfigFile(path)
	require.NoError(t, err)

	// Check defaults were applied
	assert.Equal(t, "trace.example.com", cfg.Driver.Name)
	assert.Equal(t, 1000, cfg.Driver.TotalShares)
	assert.Equal(t, 10, cfg.Driver.StepSize)
	assert.Equal(t, "otelcol.exporter.otlp.default.input", cfg.Alloy.PipelineEntryPoint)
}

func TestLoadConfigFile_Full(t *testing.T) {
	content := `apiVersion: trace.example.com/v1alpha1
kind: TraceDRADriverConfiguration
driver:
  name: trace.example.com
  totalShares: 500
  stepSize: 5
alloy:
  address: http://alloy:9090
  configDir: /custom/config
  socketDir: /custom/sockets
  pipelineEntryPoint: otelcol.processor.memory_limiter.global.input
scaling:
  bytesPerUnit: 20480
  minBytesPerSecond: 5120
  streamsPerUnit: 20
  maxRecvMsgSize: 8MiB
rateLimiting:
  decisionWait: 10s
  numTraces: 100000
reconciler:
  interval: 60s
`
	path := writeTemp(t, content)
	cfg, err := LoadConfigFile(path)
	require.NoError(t, err)

	assert.Equal(t, 500, cfg.Driver.TotalShares)
	assert.Equal(t, 5, cfg.Driver.StepSize)
	assert.Equal(t, "http://alloy:9090", cfg.Alloy.Address)
	assert.Equal(t, 20480, cfg.Scaling.BytesPerUnit)
	assert.Equal(t, 10*time.Second, cfg.RateLimiting.DecisionWait.Duration)
	assert.Equal(t, 100000, cfg.RateLimiting.NumTraces)
	assert.Equal(t, 60*time.Second, cfg.Reconciler.Interval.Duration)
}

func TestLoadConfigFile_FileNotFound(t *testing.T) {
	_, err := LoadConfigFile("/nonexistent/config.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading config file")
}

func TestLoadConfigFile_InvalidYAML(t *testing.T) {
	path := writeTemp(t, `{not valid yaml: [}`)
	_, err := LoadConfigFile(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing config file")
}

func TestLoadConfigFile_ValidationFailure(t *testing.T) {
	content := `apiVersion: trace.example.com/v1alpha1
kind: TraceDRADriverConfiguration
`
	path := writeTemp(t, content)
	_, err := LoadConfigFile(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "validating config")
	assert.Contains(t, err.Error(), "alloy.pipelineEntryPoint is required")
}

func TestLoadConfigFile_UnknownField(t *testing.T) {
	content := `apiVersion: trace.example.com/v1alpha1
kind: TraceDRADriverConfiguration
alloy:
  pipelineEntryPoint: otelcol.exporter.otlp.default.input
unknownField: true
`
	path := writeTemp(t, content)
	_, err := LoadConfigFile(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing config file")
}

// validConfig returns a TraceDRADriverConfiguration that passes validation.
func validConfig() *TraceDRADriverConfiguration {
	cfg := &TraceDRADriverConfiguration{
		TypeMeta: metav1.TypeMeta{
			APIVersion: APIVersion,
			Kind:       Kind,
		},
		Alloy: AlloySpec{
			PipelineEntryPoint: "otelcol.exporter.otlp.default.input",
		},
	}
	SetDefaults(cfg)
	return cfg
}

// writeTemp writes content to a temporary file and returns the path.
func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	return path
}
