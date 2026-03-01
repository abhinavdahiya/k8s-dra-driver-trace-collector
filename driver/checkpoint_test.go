package driver

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/abhinavdahiya/k8s-dra-driver-trace-collector/driver/config"
)

// newCheckpointTestDriver creates a Driver with checkpointPath set
// to a temp directory, suitable for checkpoint tests.
func newCheckpointTestDriver(t *testing.T) *Driver {
	t.Helper()
	cfg := &config.TraceDRADriverConfiguration{
		TypeMeta: metav1.TypeMeta{
			APIVersion: config.APIVersion,
			Kind:       config.Kind,
		},
		Driver: config.DriverSpec{
			Name:          testDriverName,
			TotalShares:   1000,
			StepSize:      10,
			CheckpointDir: t.TempDir(),
		},
		Alloy: config.AlloySpec{
			Address:            "http://127.0.0.1:12345",
			ConfigDir:          t.TempDir(),
			SocketDir:          t.TempDir(),
			PipelineEntryPoint: "otelcol.exporter.otlp.default.input",
		},
		Scaling: config.ScalingSpec{
			BytesPerUnit:      10240,
			MinBytesPerSecond: 10240,
			StreamsPerUnit:    10,
			MaxRecvMsgSize:    "4MiB",
		},
		RateLimiting: config.RateLimitingSpec{
			DecisionWait: metav1.Duration{Duration: 5 * time.Second},
			NumTraces:    50000,
		},
		Reconciler: config.ReconcilerSpec{
			Interval: metav1.Duration{Duration: 30 * time.Second},
		},
	}
	return New(DriverOptions{
		NodeName:   "node-1",
		CDIDir:     t.TempDir(),
		Config:     cfg,
		CancelFunc: func() {},
	})
}

func TestSaveCheckpoint_Basic(t *testing.T) {
	drv := newCheckpointTestDriver(t)

	// Add a claim directly.
	drv.mu.Lock()
	drv.prepared[types.UID("uid-1")] = &PreparedClaim{
		ClaimUID:  types.UID("uid-1"),
		Namespace: "default",
		Name:      "claim-1",
		Devices: []PreparedDevice{
			{
				Request:          "trace-shares",
				Pool:             "node-1",
				Device:           "trace-capacity",
				ConsumedCapacity: 50,
			},
		},
		Listener: &ListenerState{
			SocketPath:     "/var/run/alloy/uid-1/claim_uid-1.sock",
			ConfigFile:     "claim-uid-1.alloy",
			BytesPerSecond: 51200,
		},
	}
	err := drv.saveCheckpoint()
	drv.mu.Unlock()
	require.NoError(t, err)

	// Verify file exists.
	_, err = os.Stat(drv.checkpointPath)
	require.NoError(t, err, "checkpoint file should exist")

	// Verify content is valid JSON with the claim.
	data, err := os.ReadFile(drv.checkpointPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "uid-1")
	assert.Contains(t, string(data), "claim-1")
}

func TestLoadCheckpoint_Basic(t *testing.T) {
	drv := newCheckpointTestDriver(t)

	// Save a claim.
	drv.mu.Lock()
	drv.prepared[types.UID("uid-load")] = &PreparedClaim{
		ClaimUID:  types.UID("uid-load"),
		Namespace: "test-ns",
		Name:      "load-claim",
		Devices: []PreparedDevice{
			{
				Request:          "trace-shares",
				Pool:             "node-1",
				Device:           "trace-capacity",
				ConsumedCapacity: 30,
			},
		},
		Listener: &ListenerState{
			SocketPath:     "/var/run/alloy/uid-load/claim_uid-load.sock",
			ConfigFile:     "claim-uid-load.alloy",
			BytesPerSecond: 10240,
		},
	}
	err := drv.saveCheckpoint()
	drv.mu.Unlock()
	require.NoError(t, err)

	// Create a new driver pointing at the same checkpoint file.
	drv2 := newCheckpointTestDriver(t)
	drv2.checkpointPath = drv.checkpointPath

	n, err := drv2.LoadCheckpoint()
	require.NoError(t, err)
	assert.Equal(t, 1, n, "should load 1 claim")

	// Verify the loaded claim matches.
	pc, ok := drv2.prepared[types.UID("uid-load")]
	require.True(t, ok, "loaded claims should contain uid-load")
	assert.Equal(t, "test-ns", pc.Namespace)
	assert.Equal(t, "load-claim", pc.Name)
	assert.Len(t, pc.Devices, 1)
	assert.Equal(t, int64(30), pc.Devices[0].ConsumedCapacity)
	require.NotNil(t, pc.Listener)
	assert.Equal(t, 10240, pc.Listener.BytesPerSecond)
}

func TestLoadCheckpoint_MissingFile(t *testing.T) {
	drv := newCheckpointTestDriver(t)

	// Checkpoint file doesn't exist — should return 0, nil.
	n, err := drv.LoadCheckpoint()
	require.NoError(t, err)
	assert.Equal(t, 0, n, "missing file should return 0 claims")
	assert.Empty(t, drv.prepared, "prepared map should be empty")
}

func TestLoadCheckpoint_CorruptFile(t *testing.T) {
	drv := newCheckpointTestDriver(t)

	// Write garbage to the checkpoint file.
	err := os.WriteFile(drv.checkpointPath, []byte("not valid json!!!"), 0644)
	require.NoError(t, err)

	n, err := drv.LoadCheckpoint()
	assert.Error(t, err, "corrupt file should return error")
	assert.Equal(t, 0, n)
	assert.Contains(t, err.Error(), "unmarshaling checkpoint")
}

func TestCheckpoint_RoundTrip(t *testing.T) {
	drv := newCheckpointTestDriver(t)

	// Prepare multiple claims.
	drv.mu.Lock()
	drv.prepared[types.UID("rt-1")] = &PreparedClaim{
		ClaimUID:  types.UID("rt-1"),
		Namespace: "ns-a",
		Name:      "claim-a",
		Devices: []PreparedDevice{
			{Request: "trace-shares", Pool: "node-1", Device: "trace-capacity", ConsumedCapacity: 10},
			{Request: "trace-shares", Pool: "node-1", Device: "trace-capacity", ConsumedCapacity: 20},
		},
		Listener: &ListenerState{
			SocketPath:     "/var/run/alloy/rt-1/claim_rt-1.sock",
			ConfigFile:     "claim-rt-1.alloy",
			BytesPerSecond: 10240,
		},
	}
	drv.prepared[types.UID("rt-2")] = &PreparedClaim{
		ClaimUID:  types.UID("rt-2"),
		Namespace: "ns-b",
		Name:      "claim-b",
		Devices: []PreparedDevice{
			{Request: "trace-shares", Pool: "node-1", Device: "trace-capacity", ConsumedCapacity: 100},
		},
		Listener: &ListenerState{
			SocketPath:     "/var/run/alloy/rt-2/claim_rt-2.sock",
			ConfigFile:     "claim-rt-2.alloy",
			BytesPerSecond: 102400,
		},
	}
	err := drv.saveCheckpoint()
	drv.mu.Unlock()
	require.NoError(t, err)

	// Load into a fresh driver.
	drv2 := newCheckpointTestDriver(t)
	drv2.checkpointPath = drv.checkpointPath

	n, err := drv2.LoadCheckpoint()
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	// Verify rt-1.
	pc1 := drv2.prepared[types.UID("rt-1")]
	require.NotNil(t, pc1)
	assert.Equal(t, "ns-a", pc1.Namespace)
	assert.Equal(t, int64(30), pc1.TotalShares())
	assert.Len(t, pc1.Devices, 2)

	// Verify rt-2.
	pc2 := drv2.prepared[types.UID("rt-2")]
	require.NotNil(t, pc2)
	assert.Equal(t, "ns-b", pc2.Namespace)
	assert.Equal(t, int64(100), pc2.TotalShares())
	assert.Equal(t, 102400, pc2.Listener.BytesPerSecond)
}

func TestCheckpoint_EmptyPrepared(t *testing.T) {
	drv := newCheckpointTestDriver(t)

	// Save with no prepared claims.
	drv.mu.Lock()
	err := drv.saveCheckpoint()
	drv.mu.Unlock()
	require.NoError(t, err)

	// Load into fresh driver.
	drv2 := newCheckpointTestDriver(t)
	drv2.checkpointPath = drv.checkpointPath

	n, err := drv2.LoadCheckpoint()
	require.NoError(t, err)
	assert.Equal(t, 0, n, "empty checkpoint should load 0 claims")
	assert.Empty(t, drv2.prepared)
}

func TestCheckpoint_DisabledWhenEmpty(t *testing.T) {
	drv := newCheckpointTestDriver(t)
	drv.checkpointPath = "" // disable checkpoint

	drv.mu.Lock()
	err := drv.saveCheckpoint()
	drv.mu.Unlock()
	assert.NoError(t, err, "save with empty path should be no-op")

	n, err := drv.LoadCheckpoint()
	assert.NoError(t, err, "load with empty path should be no-op")
	assert.Equal(t, 0, n)
}

func TestSaveCheckpoint_AtomicOverwrite(t *testing.T) {
	drv := newCheckpointTestDriver(t)

	// Save with one claim.
	drv.mu.Lock()
	drv.prepared[types.UID("over-1")] = &PreparedClaim{
		ClaimUID: types.UID("over-1"),
		Name:     "claim-1",
		Devices:  []PreparedDevice{{ConsumedCapacity: 10}},
	}
	err := drv.saveCheckpoint()
	drv.mu.Unlock()
	require.NoError(t, err)

	// Save again with different claims (simulating unprepare + prepare).
	drv.mu.Lock()
	delete(drv.prepared, types.UID("over-1"))
	drv.prepared[types.UID("over-2")] = &PreparedClaim{
		ClaimUID: types.UID("over-2"),
		Name:     "claim-2",
		Devices:  []PreparedDevice{{ConsumedCapacity: 20}},
	}
	err = drv.saveCheckpoint()
	drv.mu.Unlock()
	require.NoError(t, err)

	// Load and verify only over-2 exists.
	drv2 := newCheckpointTestDriver(t)
	drv2.checkpointPath = drv.checkpointPath

	n, err := drv2.LoadCheckpoint()
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	_, ok := drv2.prepared[types.UID("over-1")]
	assert.False(t, ok, "over-1 should not exist after overwrite")
	_, ok = drv2.prepared[types.UID("over-2")]
	assert.True(t, ok, "over-2 should exist")
}

func TestSaveCheckpoint_FailsOnBadDir(t *testing.T) {
	drv := newCheckpointTestDriver(t)
	drv.checkpointPath = filepath.Join("/nonexistent/dir", checkpointFileName)

	drv.mu.Lock()
	err := drv.saveCheckpoint()
	drv.mu.Unlock()
	assert.Error(t, err, "save to nonexistent dir should fail")
}
