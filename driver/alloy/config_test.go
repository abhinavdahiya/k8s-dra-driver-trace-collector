package alloy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/abhinavdahiya/k8s-dra-driver-trace-collector/driver/config"
)

// testConfig returns a TraceDRADriverConfiguration with standard test values.
func testConfig() *config.TraceDRADriverConfiguration {
	return &config.TraceDRADriverConfiguration{
		TypeMeta: metav1.TypeMeta{
			APIVersion: config.APIVersion,
			Kind:       config.Kind,
		},
		Driver: config.DriverSpec{
			Name:        "trace.example.com",
			TotalShares: 1000,
			StepSize:    10,
		},
		Alloy: config.AlloySpec{
			Address:            "http://127.0.0.1:12345",
			SocketDir:          "/var/run/alloy",
			PipelineEntryPoint: "otelcol.exporter.otlp.default.input",
		},
		Scaling: config.ScalingSpec{
			SpansPerUnit:      100,
			MinSpansPerSecond: 100,
			StreamsPerUnit:    10,
			MaxRecvMsgSize:    "4MiB",
		},
		RateLimiting: config.RateLimitingSpec{
			DecisionWait: metav1.Duration{Duration: 5 * time.Second},
			NumTraces:    50000,
		},
	}
}

func TestComputeParams_SingleUnit(t *testing.T) {
	cfg := testConfig()

	params := ComputeParams("abc-123-def", 10, cfg)

	assert.Equal(t, "abc-123-def", params.ClaimUID)
	assert.Equal(t, "claim_abc_123_def", params.Label)
	assert.Equal(t, int64(10), params.Shares)
	assert.Equal(t, "/var/run/alloy/abc-123-def/claim_abc-123-def.sock", params.SocketPath)
	assert.Equal(t, 10, params.MaxConcurrentStreams)
	assert.Equal(t, "4MiB", params.MaxRecvMsgSize)
	assert.Equal(t, 100, params.SpansPerSecond)
	assert.Equal(t, "otelcol.exporter.otlp.default.input", params.PipelineEntryPoint)
	assert.Equal(t, "5s", params.DecisionWait)
	assert.Equal(t, 50000, params.NumTraces)
}

func TestComputeParams_MultipleUnits(t *testing.T) {
	cfg := testConfig()

	// 5 units = 50 shares
	params := ComputeParams("uid-5-units", 50, cfg)

	assert.Equal(t, 50, params.MaxConcurrentStreams) // 5 * 10
	assert.Equal(t, 500, params.SpansPerSecond)      // 5 * 100
}

func TestComputeParams_FloorApplied(t *testing.T) {
	cfg := testConfig()
	cfg.Scaling.SpansPerUnit = 1
	cfg.Alloy.PipelineEntryPoint = "entry.input"

	// 1 unit = 10 shares; sps = 1*1 = 1 < floor 100
	params := ComputeParams("uid-floor", 10, cfg)

	assert.Equal(t, 100, params.SpansPerSecond)
}

func TestComputeParams_ZeroShares(t *testing.T) {
	cfg := testConfig()
	cfg.Alloy.PipelineEntryPoint = "entry.input"

	// 0 shares = 0 units; floors kick in
	params := ComputeParams("uid-zero", 0, cfg)

	assert.Equal(t, 1, params.MaxConcurrentStreams) // floor
	assert.Equal(t, 100, params.SpansPerSecond)     // floor
}

func TestRenderConfig(t *testing.T) {
	params := ConfigParams{
		ClaimUID:             "abc-123-def",
		Label:                "claim_abc_123_def",
		Shares:               50,
		SocketPath:           "/var/run/alloy/abc-123-def/claim_abc-123-def.sock",
		MaxConcurrentStreams: 50,
		MaxRecvMsgSize:       "4MiB",
		SpansPerSecond:       500,
		PipelineEntryPoint:   "otelcol.exporter.otlp.default.input",
		DecisionWait:         "5s",
		NumTraces:            50000,
	}

	data, err := RenderConfig(params)
	require.NoError(t, err)

	expected := `// Managed by trace-dra-driver. Do not edit.
// Claim: abc-123-def
// Shares: 50

otelcol.receiver.otlp "claim_abc_123_def" {
  grpc {
    transport              = "unix"
    endpoint               = "/var/run/alloy/abc-123-def/claim_abc-123-def.sock"
    max_concurrent_streams = 50
    max_recv_msg_size      = "4MiB"
  }

  output {
    traces = [otelcol.processor.tail_sampling.claim_abc_123_def.input]
  }
}

otelcol.processor.tail_sampling "claim_abc_123_def" {
  decision_wait = "5s"
  num_traces    = 50000

  policy {
    name = "rate-limit"
    type = "rate_limiting"

    rate_limiting {
      spans_per_second = 500
    }
  }

  output {
    traces = [otelcol.exporter.otlp.default.input]
  }
}
`
	assert.Equal(t, expected, string(data))
}

func TestConfigFileName(t *testing.T) {
	assert.Equal(t, "claim-abc-123.alloy", ConfigFileName("abc-123"))
}

func TestWriteConfigFile(t *testing.T) {
	dir := t.TempDir()
	content := []byte("test config content")

	err := WriteConfigFile(dir, "test-uid", content)
	require.NoError(t, err)

	// Verify file exists with correct content
	data, err := os.ReadFile(filepath.Join(dir, "claim-test-uid.alloy"))
	require.NoError(t, err)
	assert.Equal(t, content, data)
}

func TestWriteConfigFile_Overwrite(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, WriteConfigFile(dir, "uid", []byte("first")))
	require.NoError(t, WriteConfigFile(dir, "uid", []byte("second")))

	data, err := os.ReadFile(filepath.Join(dir, "claim-uid.alloy"))
	require.NoError(t, err)
	assert.Equal(t, "second", string(data))
}

func TestWriteConfigFile_NoTempFileLeftOnError(t *testing.T) {
	// Write to a nonexistent directory
	err := WriteConfigFile("/nonexistent/dir", "uid", []byte("data"))
	require.Error(t, err)
}

func TestDeleteConfigFile(t *testing.T) {
	dir := t.TempDir()

	// Write then delete
	require.NoError(t, WriteConfigFile(dir, "uid", []byte("content")))
	require.NoError(t, DeleteConfigFile(dir, "uid"))

	// Verify file is gone
	_, err := os.Stat(filepath.Join(dir, "claim-uid.alloy"))
	assert.True(t, os.IsNotExist(err))
}

func TestDeleteConfigFile_Idempotent(t *testing.T) {
	dir := t.TempDir()
	// Deleting a nonexistent file should not error
	assert.NoError(t, DeleteConfigFile(dir, "nonexistent"))
}

func TestRenderConfig_LabelSanitization(t *testing.T) {
	cfg := testConfig()
	cfg.Alloy.PipelineEntryPoint = "entry.input"

	// UUID-style claim UID with hyphens
	params := ComputeParams(
		"550e8400-e29b-41d4-a716-446655440000",
		10,
		cfg,
	)

	data, err := RenderConfig(params)
	require.NoError(t, err)
	content := string(data)

	// Label should have "claim_" prefix, underscores, no hyphens
	assert.Contains(t, content, `"claim_550e8400_e29b_41d4_a716_446655440000"`)
	// Socket path should keep hyphens
	assert.Contains(t, content, "claim_550e8400-e29b-41d4-a716-446655440000.sock")

	// Ensure no raw hyphens in label positions (Alloy identifiers can't have hyphens)
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		if strings.Contains(line, `otelcol.receiver.otlp "`) ||
			strings.Contains(line, `otelcol.processor.tail_sampling "`) {
			// The label in quotes should not contain hyphens
			assert.NotContains(t, line, `"550e8400-`)
		}
	}
}

func TestSyncAdminConfig(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	// Create two .alloy files in src.
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "base.alloy"), []byte("base config"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "extra.alloy"), []byte("extra config"), 0644))
	// Non-.alloy files should be ignored.
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "readme.md"), []byte("docs"), 0644))

	changed, err := SyncAdminConfig(srcDir, dstDir)
	require.NoError(t, err)
	assert.True(t, changed, "first sync should report changed")

	// Verify copied files.
	data, err := os.ReadFile(filepath.Join(dstDir, "base.alloy"))
	require.NoError(t, err)
	assert.Equal(t, "base config", string(data))

	data, err = os.ReadFile(filepath.Join(dstDir, "extra.alloy"))
	require.NoError(t, err)
	assert.Equal(t, "extra config", string(data))

	// Non-.alloy file should NOT be copied.
	_, err = os.Stat(filepath.Join(dstDir, "readme.md"))
	assert.True(t, os.IsNotExist(err))
}

func TestSyncAdminConfig_EmptyDir(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	// No source files → no error (unlike old CopyAdminConfig),
	// just nothing to sync.
	changed, err := SyncAdminConfig(srcDir, dstDir)
	require.NoError(t, err)
	assert.False(t, changed, "empty source should report no changes")
}

func TestSyncAdminConfig_NonexistentSrc(t *testing.T) {
	dstDir := t.TempDir()

	// filepath.Glob on a nonexistent directory returns nil, nil.
	// SyncAdminConfig treats this as "no admin files to sync" — not an error.
	changed, err := SyncAdminConfig("/nonexistent/dir", dstDir)
	require.NoError(t, err)
	assert.False(t, changed, "nonexistent source should report no changes")
}

func TestSyncAdminConfig_Idempotent(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "base.alloy"), []byte("config v1"), 0644))

	// First sync: should write the file.
	changed1, err := SyncAdminConfig(srcDir, dstDir)
	require.NoError(t, err)
	assert.True(t, changed1)

	// Second sync with same content: should be a no-op.
	changed2, err := SyncAdminConfig(srcDir, dstDir)
	require.NoError(t, err)
	assert.False(t, changed2, "identical content should report no changes")

	// Update source, sync again → should detect change.
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "base.alloy"), []byte("config v2"), 0644))
	changed3, err := SyncAdminConfig(srcDir, dstDir)
	require.NoError(t, err)
	assert.True(t, changed3, "updated content should report changed")

	data, err := os.ReadFile(filepath.Join(dstDir, "base.alloy"))
	require.NoError(t, err)
	assert.Equal(t, "config v2", string(data))
}

func TestSyncAdminConfig_RemovesStaleAdminFiles(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	// Initial sync with two admin files.
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "base.alloy"), []byte("base"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "extra.alloy"), []byte("extra"), 0644))
	changed, err := SyncAdminConfig(srcDir, dstDir)
	require.NoError(t, err)
	assert.True(t, changed)

	// Remove extra.alloy from source and sync again.
	require.NoError(t, os.Remove(filepath.Join(srcDir, "extra.alloy")))

	// Also place a claim-managed file in dstDir — should NOT be removed.
	require.NoError(t, os.WriteFile(filepath.Join(dstDir, "claim-uid-1.alloy"), []byte("claim config"), 0644))

	changed, err = SyncAdminConfig(srcDir, dstDir)
	require.NoError(t, err)
	assert.True(t, changed, "removing stale file should report changed")

	// extra.alloy should be gone from dst.
	_, err = os.Stat(filepath.Join(dstDir, "extra.alloy"))
	assert.True(t, os.IsNotExist(err), "stale admin file should be removed")

	// base.alloy should still exist.
	_, err = os.Stat(filepath.Join(dstDir, "base.alloy"))
	assert.NoError(t, err, "base.alloy should still exist")

	// claim-managed file should NOT be removed.
	_, err = os.Stat(filepath.Join(dstDir, "claim-uid-1.alloy"))
	assert.NoError(t, err, "claim-managed file should not be removed by admin sync")
}
