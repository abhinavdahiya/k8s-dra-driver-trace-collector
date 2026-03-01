package driver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"
	"k8s.io/utils/ptr"

	"github.com/abhinavdahiya/k8s-dra-driver-trace-collector/driver/config"
)

const testDriverName = "trace.example.com"

// fakeExplicitClaim creates a ResourceClaim with 1 allocation result
// consuming the specified number of shares (explicit claim pattern).
func fakeExplicitClaim(uid, name, ns string, shares int64) *resourceapi.ResourceClaim {
	return &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			UID:       types.UID(uid),
			Name:      name,
			Namespace: ns,
		},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{
				Devices: resourceapi.DeviceAllocationResult{
					Results: []resourceapi.DeviceRequestAllocationResult{
						{
							Request: "trace-shares",
							Driver:  testDriverName,
							Pool:    "node-1",
							Device:  "trace-capacity",
							ShareID: ptr.To(types.UID("share-abc")),
							ConsumedCapacity: map[resourceapi.QualifiedName]resource.Quantity{
								"shares": resource.MustParse(strconv.FormatInt(shares, 10)),
							},
						},
					},
				},
			},
		},
	}
}

// fakeExtendedResourceClaim creates a ResourceClaim with count allocation
// results, each consuming 1 share (extended resource pattern).
func fakeExtendedResourceClaim(uid, name, ns string, count int) *resourceapi.ResourceClaim {
	results := make([]resourceapi.DeviceRequestAllocationResult, count)
	for i := range count {
		results[i] = resourceapi.DeviceRequestAllocationResult{
			Request: "trace-shares",
			Driver:  testDriverName,
			Pool:    "node-1",
			Device:  "trace-capacity",
			ShareID: ptr.To(types.UID(fmt.Sprintf("share-%04d", i))),
			ConsumedCapacity: map[resourceapi.QualifiedName]resource.Quantity{
				"shares": resource.MustParse("1"),
			},
		}
	}
	return &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			UID:       types.UID(uid),
			Name:      name,
			Namespace: ns,
		},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{
				Devices: resourceapi.DeviceAllocationResult{
					Results: results,
				},
			},
		},
	}
}

func newTestDriver(t *testing.T) *Driver {
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
	})
}

func TestPrepare_ExplicitClaim(t *testing.T) {
	drv := newTestDriver(t)
	claim := fakeExplicitClaim("uid-1", "my-traces", "default", 50)

	results, err := drv.PrepareResourceClaims(context.Background(), []*resourceapi.ResourceClaim{claim})
	require.NoError(t, err)
	require.Contains(t, results, types.UID("uid-1"))

	r := results[types.UID("uid-1")]
	assert.NoError(t, r.Err)
	assert.Len(t, r.Devices, 1)
	assert.Equal(t, "trace-shares", r.Devices[0].Requests[0])
	assert.Equal(t, "node-1", r.Devices[0].PoolName)
	assert.Equal(t, "trace-capacity", r.Devices[0].DeviceName)

	// Verify internal state.
	pc := drv.prepared[types.UID("uid-1")]
	require.NotNil(t, pc)
	assert.Len(t, pc.Devices, 1)
	assert.Equal(t, int64(50), pc.Devices[0].ConsumedCapacity)
	assert.Equal(t, int64(50), pc.TotalShares())
}

func TestPrepare_ExtendedResource(t *testing.T) {
	drv := newTestDriver(t)
	claim := fakeExtendedResourceClaim("uid-2", "auto-claim", "default", 50)

	results, err := drv.PrepareResourceClaims(context.Background(), []*resourceapi.ResourceClaim{claim})
	require.NoError(t, err)
	require.Contains(t, results, types.UID("uid-2"))

	r := results[types.UID("uid-2")]
	assert.NoError(t, r.Err)
	assert.Len(t, r.Devices, 50)

	// Verify internal state.
	pc := drv.prepared[types.UID("uid-2")]
	require.NotNil(t, pc)
	assert.Len(t, pc.Devices, 50)
	assert.Equal(t, int64(50), pc.TotalShares())
}

func TestPrepare_Idempotent(t *testing.T) {
	drv := newTestDriver(t)
	claim := fakeExplicitClaim("uid-3", "idem-claim", "default", 25)

	// First call.
	results1, err := drv.PrepareResourceClaims(context.Background(), []*resourceapi.ResourceClaim{claim})
	require.NoError(t, err)
	require.NoError(t, results1[types.UID("uid-3")].Err)

	// Second call: same claim.
	results2, err := drv.PrepareResourceClaims(context.Background(), []*resourceapi.ResourceClaim{claim})
	require.NoError(t, err)
	require.NoError(t, results2[types.UID("uid-3")].Err)

	// Both return same device count.
	assert.Len(t, results1[types.UID("uid-3")].Devices, 1)
	assert.Len(t, results2[types.UID("uid-3")].Devices, 1)

	// Map still has only 1 entry.
	assert.Len(t, drv.prepared, 1)
}

func TestPrepare_MultiClaim(t *testing.T) {
	drv := newTestDriver(t)
	claims := []*resourceapi.ResourceClaim{
		fakeExplicitClaim("uid-a", "claim-a", "ns-1", 10),
		fakeExplicitClaim("uid-b", "claim-b", "ns-1", 20),
		fakeExtendedResourceClaim("uid-c", "claim-c", "ns-2", 5),
	}

	results, err := drv.PrepareResourceClaims(context.Background(), claims)
	require.NoError(t, err)

	for _, uid := range []types.UID{"uid-a", "uid-b", "uid-c"} {
		require.Contains(t, results, uid)
		assert.NoError(t, results[uid].Err)
	}
	assert.Len(t, drv.prepared, 3)
}

func TestPrepare_NoAllocation(t *testing.T) {
	drv := newTestDriver(t)
	claim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			UID:       types.UID("uid-nil"),
			Name:      "no-alloc",
			Namespace: "default",
		},
		// Status.Allocation is nil.
	}

	results, err := drv.PrepareResourceClaims(context.Background(), []*resourceapi.ResourceClaim{claim})
	require.NoError(t, err)
	require.Contains(t, results, types.UID("uid-nil"))
	assert.Error(t, results[types.UID("uid-nil")].Err)
	assert.Contains(t, results[types.UID("uid-nil")].Err.Error(), "no allocation")
}

func TestPrepare_WrongDriver(t *testing.T) {
	drv := newTestDriver(t)
	claim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			UID:       types.UID("uid-wrong"),
			Name:      "wrong-driver",
			Namespace: "default",
		},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{
				Devices: resourceapi.DeviceAllocationResult{
					Results: []resourceapi.DeviceRequestAllocationResult{
						{
							Request: "some-req",
							Driver:  "other.driver.io",
							Pool:    "node-1",
							Device:  "some-device",
						},
					},
				},
			},
		},
	}

	results, err := drv.PrepareResourceClaims(context.Background(), []*resourceapi.ResourceClaim{claim})
	require.NoError(t, err)
	require.Contains(t, results, types.UID("uid-wrong"))
	assert.Error(t, results[types.UID("uid-wrong")].Err)
	assert.Contains(t, results[types.UID("uid-wrong")].Err.Error(), "no allocation results for driver")
}

func TestPrepare_ShareID(t *testing.T) {
	drv := newTestDriver(t)
	claim := fakeExplicitClaim("uid-share", "share-claim", "default", 10)

	results, err := drv.PrepareResourceClaims(context.Background(), []*resourceapi.ResourceClaim{claim})
	require.NoError(t, err)
	require.NoError(t, results[types.UID("uid-share")].Err)

	pc := drv.prepared[types.UID("uid-share")]
	require.NotNil(t, pc)
	assert.Equal(t, "share-abc", pc.Devices[0].ShareID)
}

func TestUnprepare_Basic(t *testing.T) {
	drv := newTestDriver(t)
	claim := fakeExplicitClaim("uid-unprep", "unprep-claim", "default", 30)

	// Prepare first.
	_, err := drv.PrepareResourceClaims(context.Background(), []*resourceapi.ResourceClaim{claim})
	require.NoError(t, err)
	require.Len(t, drv.prepared, 1)

	// Unprepare.
	results, err := drv.UnprepareResourceClaims(context.Background(), []kubeletplugin.NamespacedObject{
		{NamespacedName: types.NamespacedName{Name: "unprep-claim", Namespace: "default"}, UID: types.UID("uid-unprep")},
	})
	require.NoError(t, err)
	assert.NoError(t, results[types.UID("uid-unprep")])
	assert.Empty(t, drv.prepared)
}

func TestUnprepare_Idempotent(t *testing.T) {
	drv := newTestDriver(t)

	// Unprepare a claim that was never prepared.
	results, err := drv.UnprepareResourceClaims(context.Background(), []kubeletplugin.NamespacedObject{
		{NamespacedName: types.NamespacedName{Name: "never-prepared", Namespace: "default"}, UID: types.UID("uid-never")},
	})
	require.NoError(t, err)
	assert.NoError(t, results[types.UID("uid-never")])
}

func TestUnprepare_Unknown(t *testing.T) {
	drv := newTestDriver(t)

	// Unprepare a random UID.
	results, err := drv.UnprepareResourceClaims(context.Background(), []kubeletplugin.NamespacedObject{
		{NamespacedName: types.NamespacedName{Name: "random", Namespace: "default"}, UID: types.UID("uid-random-12345")},
	})
	require.NoError(t, err)
	assert.NoError(t, results[types.UID("uid-random-12345")])
}

func TestTotalShares(t *testing.T) {
	pc := &PreparedClaim{
		Devices: []PreparedDevice{
			{ConsumedCapacity: 10},
			{ConsumedCapacity: 25},
			{ConsumedCapacity: 5},
			{ConsumedCapacity: 60},
		},
	}
	assert.Equal(t, int64(100), pc.TotalShares())
}

func TestPrepare_WritesCDISpec(t *testing.T) {
	drv := newTestDriver(t)
	claim := fakeExplicitClaim("uid-cdi", "cdi-claim", "default", 50)

	results, err := drv.PrepareResourceClaims(context.Background(), []*resourceapi.ResourceClaim{claim})
	require.NoError(t, err)
	require.NoError(t, results[types.UID("uid-cdi")].Err)

	// CDI device ID should be set on the result.
	r := results[types.UID("uid-cdi")]
	require.Len(t, r.Devices, 1)
	assert.Equal(t, []string{"trace.example.com/trace=uid-cdi"}, r.Devices[0].CDIDeviceIDs)

	// Verify CDI spec file exists in cdiDir.
	cdiFile := filepath.Join(drv.cdiDir, "trace-uid-cdi.json")
	_, err = os.Stat(cdiFile)
	assert.NoError(t, err, "CDI spec file should exist")
}

func TestPrepare_ListenerState(t *testing.T) {
	drv := newTestDriver(t)
	claim := fakeExplicitClaim("uid-ls", "ls-claim", "default", 50)

	_, err := drv.PrepareResourceClaims(context.Background(), []*resourceapi.ResourceClaim{claim})
	require.NoError(t, err)

	pc := drv.prepared[types.UID("uid-ls")]
	require.NotNil(t, pc)
	require.NotNil(t, pc.Listener)
	assert.Equal(t, filepath.Join(drv.cfg.Alloy.SocketDir, "uid-ls", "claim_uid-ls.sock"), pc.Listener.SocketPath)
	assert.Equal(t, "claim-uid-ls.alloy", pc.Listener.ConfigFile)

	// Verify the per-claim socket directory was created.
	info, err := os.Stat(filepath.Join(drv.cfg.Alloy.SocketDir, "uid-ls"))
	require.NoError(t, err, "socket directory should exist after Prepare")
	assert.True(t, info.IsDir(), "socket path should be a directory")
}

func TestPrepare_TriggersReconcile(t *testing.T) {
	drv := newTestDriver(t)
	claim := fakeExplicitClaim("uid-rec", "rec-claim", "default", 10)

	_, err := drv.PrepareResourceClaims(context.Background(), []*resourceapi.ResourceClaim{claim})
	require.NoError(t, err)

	// Workqueue should have the reconcile key queued.
	assert.Equal(t, 1, drv.queue.Len(), "expected reconcile key in workqueue after Prepare")
}

func TestUnprepare_TriggersReconcile(t *testing.T) {
	drv := newTestDriver(t)
	claim := fakeExplicitClaim("uid-unrec", "unrec-claim", "default", 10)

	_, err := drv.PrepareResourceClaims(context.Background(), []*resourceapi.ResourceClaim{claim})
	require.NoError(t, err)

	// Drain the prepare trigger.
	key, shutdown := drv.queue.Get()
	require.False(t, shutdown)
	drv.queue.Done(key)

	_, err = drv.UnprepareResourceClaims(context.Background(), []kubeletplugin.NamespacedObject{
		{NamespacedName: types.NamespacedName{Name: "unrec-claim", Namespace: "default"}, UID: types.UID("uid-unrec")},
	})
	require.NoError(t, err)

	assert.Equal(t, 1, drv.queue.Len(), "expected reconcile key in workqueue after Unprepare")
}
