package driver

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"
	"k8s.io/utils/ptr"
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

func newTestDriver() *Driver {
	return New("node-1", testDriverName, 1000, func() {})
}

func TestPrepare_ExplicitClaim(t *testing.T) {
	drv := newTestDriver()
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
	drv := newTestDriver()
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
	drv := newTestDriver()
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
	drv := newTestDriver()
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
	drv := newTestDriver()
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
	drv := newTestDriver()
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
	drv := newTestDriver()
	claim := fakeExplicitClaim("uid-share", "share-claim", "default", 10)

	results, err := drv.PrepareResourceClaims(context.Background(), []*resourceapi.ResourceClaim{claim})
	require.NoError(t, err)
	require.NoError(t, results[types.UID("uid-share")].Err)

	pc := drv.prepared[types.UID("uid-share")]
	require.NotNil(t, pc)
	assert.Equal(t, "share-abc", pc.Devices[0].ShareID)
}

func TestUnprepare_Basic(t *testing.T) {
	drv := newTestDriver()
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
	drv := newTestDriver()

	// Unprepare a claim that was never prepared.
	results, err := drv.UnprepareResourceClaims(context.Background(), []kubeletplugin.NamespacedObject{
		{NamespacedName: types.NamespacedName{Name: "never-prepared", Namespace: "default"}, UID: types.UID("uid-never")},
	})
	require.NoError(t, err)
	assert.NoError(t, results[types.UID("uid-never")])
}

func TestUnprepare_Unknown(t *testing.T) {
	drv := newTestDriver()

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
