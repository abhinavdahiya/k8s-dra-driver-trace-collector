package driver

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/abhinavdahiya/k8s-dra-driver-trace-collector/driver/alloy"
	"github.com/abhinavdahiya/k8s-dra-driver-trace-collector/driver/cdi"
	"github.com/abhinavdahiya/k8s-dra-driver-trace-collector/driver/config"
)

// fakeReloader records Reload calls for testing.
type fakeReloader struct {
	calls int
	err   error // if set, Reload returns this error
}

func (f *fakeReloader) Reload(_ context.Context) error {
	f.calls++
	return f.err
}

// newReconcileTestDriver creates a Driver wired to temp directories
// and a fakeReloader, ready for reconcile tests.
func newReconcileTestDriver(t *testing.T, reloader *fakeReloader) *Driver {
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
			SpansPerUnit:      100,
			MinSpansPerSecond: 100,
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
		Reloader:   reloader,
	})
}

// addPreparedClaim adds a PreparedClaim directly to the driver's
// prepared map (bypassing Prepare flow) for reconcile testing.
func addPreparedClaim(drv *Driver, uid string, shares int64) {
	drv.mu.Lock()
	defer drv.mu.Unlock()

	params := alloy.ComputeParams(uid, shares, drv.cfg)
	drv.prepared[types.UID(uid)] = &PreparedClaim{
		ClaimUID:  types.UID(uid),
		Namespace: "default",
		Name:      "claim-" + uid,
		Devices: []PreparedDevice{
			{
				Request:          "trace-shares",
				Pool:             "node-1",
				Device:           "trace-capacity",
				ConsumedCapacity: shares,
			},
		},
		Listener: &ListenerState{
			SocketPath:     params.SocketPath,
			ConfigFile:     alloy.ConfigFileName(uid),
			SpansPerSecond: params.SpansPerSecond,
		},
	}
}

// renderExpectedConfig renders the expected config for a claim UID
// using the driver's scaling config.
func renderExpectedConfig(drv *Driver, uid string, shares int64) []byte {
	params := alloy.ComputeParams(uid, shares, drv.cfg)
	content, err := alloy.RenderConfig(params)
	if err != nil {
		panic(fmt.Sprintf("renderExpectedConfig: %v", err))
	}
	return content
}

// writeOrphanConfig writes an alloy config file that is not associated
// with any prepared claim.
func writeOrphanConfig(t *testing.T, configDir, uid string) {
	t.Helper()
	content := []byte("// orphan config for " + uid)
	err := os.WriteFile(filepath.Join(configDir, alloy.ConfigFileName(uid)), content, 0644)
	require.NoError(t, err)
}

// writeOrphanCDI writes a CDI spec file that is not associated
// with any prepared claim.
func writeOrphanCDI(t *testing.T, cdiDir, uid string) {
	t.Helper()
	content := []byte(`{"cdiVersion":"0.6.0","kind":"trace.example.com/trace","devices":[]}`)
	err := os.WriteFile(filepath.Join(cdiDir, "trace-"+uid+".json"), content, 0644)
	require.NoError(t, err)
}

// writeOrphanSocketDir creates a per-claim socket directory that is
// not associated with any prepared claim.
func writeOrphanSocketDir(t *testing.T, socketDir, uid string) {
	t.Helper()
	dir := filepath.Join(socketDir, uid)
	require.NoError(t, os.Mkdir(dir, 0755))
	// Place a dummy socket file inside.
	err := os.WriteFile(filepath.Join(dir, "claim_"+uid+".sock"), []byte{}, 0644)
	require.NoError(t, err)
}

// --- Tests ---

func TestReconcile_CleanState(t *testing.T) {
	reloader := &fakeReloader{}
	drv := newReconcileTestDriver(t, reloader)

	// Add a claim and write correct config + CDI spec.
	uid := "clean-uid-1"
	var shares int64 = 50
	addPreparedClaim(drv, uid, shares)

	expected := renderExpectedConfig(drv, uid, shares)
	require.NoError(t, alloy.WriteConfigFile(drv.cfg.Alloy.ConfigDir, uid, expected))

	params := alloy.ComputeParams(uid, shares, drv.cfg)
	require.NoError(t, cdi.WriteSpec(drv.cdiDir, uid, params.SocketPath))

	// Reconcile should be a no-op.
	err := drv.Reconcile(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, reloader.calls, "no reload when state is clean")

	// Files should still exist.
	_, err = os.Stat(filepath.Join(drv.cfg.Alloy.ConfigDir, alloy.ConfigFileName(uid)))
	assert.NoError(t, err)
	_, err = os.Stat(filepath.Join(drv.cdiDir, "trace-"+uid+".json"))
	assert.NoError(t, err)
}

func TestReconcile_MissingConfigFile(t *testing.T) {
	reloader := &fakeReloader{}
	drv := newReconcileTestDriver(t, reloader)

	uid := "missing-cfg-1"
	var shares int64 = 50
	addPreparedClaim(drv, uid, shares)

	// CDI spec exists, but config file does NOT.
	params := alloy.ComputeParams(uid, shares, drv.cfg)
	require.NoError(t, cdi.WriteSpec(drv.cdiDir, uid, params.SocketPath))

	err := drv.Reconcile(context.Background())
	require.NoError(t, err)

	// Config file should now exist with correct content.
	actual, err := os.ReadFile(filepath.Join(drv.cfg.Alloy.ConfigDir, alloy.ConfigFileName(uid)))
	require.NoError(t, err)
	expected := renderExpectedConfig(drv, uid, shares)
	assert.True(t, bytes.Equal(expected, actual), "config content mismatch")

	// Reload should have been called once.
	assert.Equal(t, 1, reloader.calls, "reload expected after writing missing config")
}

func TestReconcile_OrphanConfigFile(t *testing.T) {
	reloader := &fakeReloader{}
	drv := newReconcileTestDriver(t, reloader)

	// No claims prepared, but an orphan config file exists.
	orphanUID := "orphan-cfg-1"
	writeOrphanConfig(t, drv.cfg.Alloy.ConfigDir, orphanUID)

	err := drv.Reconcile(context.Background())
	require.NoError(t, err)

	// Orphan config file should be deleted.
	_, err = os.Stat(filepath.Join(drv.cfg.Alloy.ConfigDir, alloy.ConfigFileName(orphanUID)))
	assert.True(t, os.IsNotExist(err), "orphan config should be deleted")

	// Reload because a config file was deleted.
	assert.Equal(t, 1, reloader.calls, "reload expected after deleting orphan config")
}

func TestReconcile_StaleConfigContent(t *testing.T) {
	reloader := &fakeReloader{}
	drv := newReconcileTestDriver(t, reloader)

	uid := "stale-cfg-1"
	var shares int64 = 50
	addPreparedClaim(drv, uid, shares)

	// Write config with wrong content (stale).
	staleContent := []byte("// stale config content")
	require.NoError(t, alloy.WriteConfigFile(drv.cfg.Alloy.ConfigDir, uid, staleContent))

	// CDI spec exists.
	params := alloy.ComputeParams(uid, shares, drv.cfg)
	require.NoError(t, cdi.WriteSpec(drv.cdiDir, uid, params.SocketPath))

	err := drv.Reconcile(context.Background())
	require.NoError(t, err)

	// Config should now have correct content.
	actual, err := os.ReadFile(filepath.Join(drv.cfg.Alloy.ConfigDir, alloy.ConfigFileName(uid)))
	require.NoError(t, err)
	expected := renderExpectedConfig(drv, uid, shares)
	assert.True(t, bytes.Equal(expected, actual), "stale config should be overwritten")

	// Reload triggered.
	assert.Equal(t, 1, reloader.calls, "reload expected after overwriting stale config")
}

func TestReconcile_MissingCDISpec(t *testing.T) {
	reloader := &fakeReloader{}
	drv := newReconcileTestDriver(t, reloader)

	uid := "missing-cdi-1"
	var shares int64 = 50
	addPreparedClaim(drv, uid, shares)

	// Config file exists with correct content, but CDI spec is missing.
	expected := renderExpectedConfig(drv, uid, shares)
	require.NoError(t, alloy.WriteConfigFile(drv.cfg.Alloy.ConfigDir, uid, expected))

	err := drv.Reconcile(context.Background())
	require.NoError(t, err)

	// CDI spec should now exist.
	_, err = os.Stat(filepath.Join(drv.cdiDir, "trace-"+uid+".json"))
	assert.NoError(t, err, "CDI spec should be regenerated")

	// No reload needed (only CDI was missing, not config).
	assert.Equal(t, 0, reloader.calls, "no reload for CDI-only change")
}

func TestReconcile_OrphanCDISpec(t *testing.T) {
	reloader := &fakeReloader{}
	drv := newReconcileTestDriver(t, reloader)

	// No claims, but orphan CDI spec exists.
	orphanUID := "orphan-cdi-1"
	writeOrphanCDI(t, drv.cdiDir, orphanUID)

	err := drv.Reconcile(context.Background())
	require.NoError(t, err)

	// Orphan CDI spec should be deleted.
	_, err = os.Stat(filepath.Join(drv.cdiDir, "trace-"+orphanUID+".json"))
	assert.True(t, os.IsNotExist(err), "orphan CDI spec should be deleted")

	// No reload needed (CDI changes don't require Alloy reload).
	assert.Equal(t, 0, reloader.calls, "no reload for CDI-only cleanup")
}

func TestReconcile_OrphanSocket(t *testing.T) {
	reloader := &fakeReloader{}
	drv := newReconcileTestDriver(t, reloader)

	// No claims, but orphan socket file exists.
	orphanUID := "orphan-sock-1"
	writeOrphanSocketDir(t, drv.cfg.Alloy.SocketDir, orphanUID)

	err := drv.Reconcile(context.Background())
	require.NoError(t, err)

	// Orphan socket directory should be deleted.
	_, err = os.Stat(filepath.Join(drv.cfg.Alloy.SocketDir, orphanUID))
	assert.True(t, os.IsNotExist(err), "orphan socket directory should be deleted")

	// No reload (socket changes don't require Alloy reload).
	assert.Equal(t, 0, reloader.calls, "no reload for socket-only cleanup")
}

func TestReconcile_MultipleIssues(t *testing.T) {
	reloader := &fakeReloader{}
	drv := newReconcileTestDriver(t, reloader)

	// Claim 1: missing config + missing CDI.
	uid1 := "multi-1"
	addPreparedClaim(drv, uid1, 50)

	// Claim 2: stale config, CDI exists.
	uid2 := "multi-2"
	addPreparedClaim(drv, uid2, 100)
	require.NoError(t, alloy.WriteConfigFile(drv.cfg.Alloy.ConfigDir, uid2, []byte("// stale")))
	params2 := alloy.ComputeParams(uid2, 100, drv.cfg)
	require.NoError(t, cdi.WriteSpec(drv.cdiDir, uid2, params2.SocketPath))

	// Orphans: config file, CDI spec, socket.
	writeOrphanConfig(t, drv.cfg.Alloy.ConfigDir, "orphan-multi-1")
	writeOrphanCDI(t, drv.cdiDir, "orphan-multi-2")
	writeOrphanSocketDir(t, drv.cfg.Alloy.SocketDir, "orphan-multi-3")

	err := drv.Reconcile(context.Background())
	require.NoError(t, err)

	// Claim 1 config should exist with correct content.
	actual1, err := os.ReadFile(filepath.Join(drv.cfg.Alloy.ConfigDir, alloy.ConfigFileName(uid1)))
	require.NoError(t, err)
	expected1 := renderExpectedConfig(drv, uid1, 50)
	assert.True(t, bytes.Equal(expected1, actual1))

	// Claim 2 config should be overwritten.
	actual2, err := os.ReadFile(filepath.Join(drv.cfg.Alloy.ConfigDir, alloy.ConfigFileName(uid2)))
	require.NoError(t, err)
	expected2 := renderExpectedConfig(drv, uid2, 100)
	assert.True(t, bytes.Equal(expected2, actual2))

	// CDI specs for both claims should exist.
	_, err = os.Stat(filepath.Join(drv.cdiDir, "trace-"+uid1+".json"))
	assert.NoError(t, err)
	_, err = os.Stat(filepath.Join(drv.cdiDir, "trace-"+uid2+".json"))
	assert.NoError(t, err)

	// Orphan files should all be deleted.
	_, err = os.Stat(filepath.Join(drv.cfg.Alloy.ConfigDir, alloy.ConfigFileName("orphan-multi-1")))
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(drv.cdiDir, "trace-orphan-multi-2.json"))
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(drv.cfg.Alloy.SocketDir, "orphan-multi-3"))
	assert.True(t, os.IsNotExist(err))

	// Single reload for all config changes combined.
	assert.Equal(t, 1, reloader.calls, "should issue exactly one reload for multiple config changes")
}

func TestReconcile_EmptyDesired(t *testing.T) {
	reloader := &fakeReloader{}
	drv := newReconcileTestDriver(t, reloader)

	// No claims, but orphan files exist for everything.
	writeOrphanConfig(t, drv.cfg.Alloy.ConfigDir, "empty-1")
	writeOrphanConfig(t, drv.cfg.Alloy.ConfigDir, "empty-2")
	writeOrphanCDI(t, drv.cdiDir, "empty-1")
	writeOrphanCDI(t, drv.cdiDir, "empty-3")
	writeOrphanSocketDir(t, drv.cfg.Alloy.SocketDir, "empty-1")

	err := drv.Reconcile(context.Background())
	require.NoError(t, err)

	// All orphans should be cleaned up.
	entries, _ := os.ReadDir(drv.cfg.Alloy.ConfigDir)
	assert.Empty(t, entries, "configDir should be empty after cleanup")

	entries, _ = os.ReadDir(drv.cdiDir)
	assert.Empty(t, entries, "cdiDir should be empty after cleanup")

	entries, _ = os.ReadDir(drv.cfg.Alloy.SocketDir)
	assert.Empty(t, entries, "socketDir should be empty after cleanup")

	// Reload because config files were deleted.
	assert.Equal(t, 1, reloader.calls, "reload expected after deleting orphan configs")
}

func TestReconcile_ReloadFailure(t *testing.T) {
	reloader := &fakeReloader{err: fmt.Errorf("alloy returned 400: bad config")}
	drv := newReconcileTestDriver(t, reloader)

	uid := "reload-fail-1"
	addPreparedClaim(drv, uid, 50)

	// No config file on disk → reconciler will write it → trigger reload → reload fails.
	err := drv.Reconcile(context.Background())
	assert.Error(t, err, "reconcile should return reload error")
	assert.Contains(t, err.Error(), "bad config")

	// Config file should still have been written (only reload failed).
	_, statErr := os.Stat(filepath.Join(drv.cfg.Alloy.ConfigDir, alloy.ConfigFileName(uid)))
	assert.NoError(t, statErr, "config file should exist despite reload failure")

	assert.Equal(t, 1, reloader.calls)

	// Next reconcile call retries the reload (config is now clean → no dirty,
	// but CDI was also missing and will be written).
	reloader.err = nil
	err = drv.Reconcile(context.Background())
	assert.NoError(t, err, "retry should succeed")

	// CDI spec should exist after retry.
	_, statErr = os.Stat(filepath.Join(drv.cdiDir, "trace-"+uid+".json"))
	assert.NoError(t, statErr, "CDI spec should exist after retry")
}

func TestReconcile_Idempotent(t *testing.T) {
	reloader := &fakeReloader{}
	drv := newReconcileTestDriver(t, reloader)

	uid := "idem-1"
	var shares int64 = 50
	addPreparedClaim(drv, uid, shares)

	// First reconcile: creates config + CDI, triggers reload.
	err := drv.Reconcile(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, reloader.calls)

	// Second reconcile: everything is clean, no changes.
	err = drv.Reconcile(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, reloader.calls, "no additional reload on idempotent call")

	// Third reconcile: still clean.
	err = drv.Reconcile(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, reloader.calls, "still no additional reload")
}

func TestTriggerReconcile_Coalescing(t *testing.T) {
	reloader := &fakeReloader{}
	drv := newReconcileTestDriver(t, reloader)

	// Fire many rapid triggers.
	for range 100 {
		drv.triggerReconcile()
	}

	// Workqueue deduplicates: only 1 item should be pending.
	// (workqueue may show more if processed in between, but
	// with no consumer running, all 100 coalesce to 1).
	assert.Equal(t, 1, drv.queue.Len(),
		"rapid triggers should coalesce to 1 queued item")

	// Drain it.
	key, shutdown := drv.queue.Get()
	assert.False(t, shutdown)
	assert.Equal(t, reconcileKey, key)
	drv.queue.Done(key)

	assert.Equal(t, 0, drv.queue.Len())
}

func TestReconciler_Shutdown(t *testing.T) {
	reloader := &fakeReloader{}
	drv := newReconcileTestDriver(t, reloader)

	// Use a short reconcile interval so the timer fires at least once.
	drv.cfg.Reconciler.Interval.Duration = 50 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())

	// Track reconcile calls via reloader (each reconcile on empty state = no reload).
	var reconcileCount atomic.Int32

	// Replace reloader with one that counts.
	countingReloader := &fakeReloader{}
	drv.reloader = countingReloader

	// Add a claim so reconcile does work (writes config → triggers reload).
	addPreparedClaim(drv, "shutdown-1", 50)

	// Override reloader to count reconcile cycles.
	trackingReloader := &fakeReloader{}
	drv.reloader = trackingReloader

	drv.StartReconciler(ctx)

	// Let the reconciler run a few cycles.
	time.Sleep(200 * time.Millisecond)
	reconcileCount.Store(int32(trackingReloader.calls))

	// Cancel context to trigger shutdown.
	cancel()

	// Give goroutines time to exit.
	time.Sleep(100 * time.Millisecond)

	// After shutdown, queue should be shut down (Get returns shutdown=true).
	_, shutdown := drv.queue.Get()
	assert.True(t, shutdown, "queue should be shut down after context cancellation")

	// At least one reconcile should have run (from either timer or queue).
	assert.Greater(t, reconcileCount.Load(), int32(0),
		"reconciler should have processed at least one cycle before shutdown")
}

// --- scanGlob tests ---

func TestScanGlob_Empty(t *testing.T) {
	dir := t.TempDir()
	result := scanGlob(dir, "claim-*.alloy", "claim-", ".alloy")
	assert.Empty(t, result)
}

func TestScanGlob_MatchesFiles(t *testing.T) {
	dir := t.TempDir()

	// Create matching files.
	for _, uid := range []string{"uid-1", "uid-2", "uid-3"} {
		err := os.WriteFile(filepath.Join(dir, "claim-"+uid+".alloy"), []byte("test"), 0644)
		require.NoError(t, err)
	}

	// Create a non-matching file.
	err := os.WriteFile(filepath.Join(dir, "other-file.txt"), []byte("test"), 0644)
	require.NoError(t, err)

	result := scanGlob(dir, "claim-*.alloy", "claim-", ".alloy")
	assert.Len(t, result, 3)
	assert.Contains(t, result, "uid-1")
	assert.Contains(t, result, "uid-2")
	assert.Contains(t, result, "uid-3")
}

func TestScanGlob_NonExistentDir(t *testing.T) {
	result := scanGlob("/nonexistent/path", "claim-*.alloy", "claim-", ".alloy")
	assert.Empty(t, result)
}
