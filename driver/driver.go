// Package driver implements the DRA kubelet plugin for trace capacity management.
package driver

import (
	"context"
	"errors"
	"fmt"
	"sync"

	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"
	"k8s.io/klog/v2"
)

// Compile-time interface assertion.
var _ kubeletplugin.DRAPlugin = (*Driver)(nil)

// Driver implements the kubeletplugin.DRAPlugin interface for the
// trace capacity DRA driver. It publishes a single device with
// consumable shares and handles Prepare/Unprepare lifecycle calls.
type Driver struct {
	nodeName       string
	driverName     string
	totalShares    int
	checkpointPath string
	cancelFunc     context.CancelFunc

	mu       sync.Mutex
	prepared map[types.UID]*PreparedClaim
}

// PreparedClaim is the driver's record of a claim that has been
// prepared on this node. It stores one PreparedDevice per
// DeviceRequestAllocationResult that belongs to this driver.
type PreparedClaim struct {
	ClaimUID  types.UID
	Namespace string
	Name      string
	Devices   []PreparedDevice
}

// PreparedDevice records a single allocation result from the
// scheduler. For explicit ResourceClaims with consumable capacity,
// there is typically 1 device with a large ConsumedCapacity. For
// extended resource claims, there are N devices each consuming the
// default (1 share).
type PreparedDevice struct {
	Request          string // original request name
	Pool             string // pool name (= node name)
	Device           string // device name ("trace-capacity")
	ShareID          string // unique share allocation ID (from scheduler)
	ConsumedCapacity int64  // shares consumed by this allocation result
}

// TotalShares returns the sum of ConsumedCapacity across all devices
// in this claim.
func (pc *PreparedClaim) TotalShares() int64 {
	var total int64
	for _, d := range pc.Devices {
		total += d.ConsumedCapacity
	}
	return total
}

// New creates a new Driver instance.
func New(nodeName, driverName string, totalShares int, cancel context.CancelFunc) *Driver {
	return &Driver{
		nodeName:    nodeName,
		driverName:  driverName,
		totalShares: totalShares,
		cancelFunc:  cancel,
		prepared:    make(map[types.UID]*PreparedClaim),
	}
}

// PrepareResourceClaims handles the claim preparation lifecycle. For each
// claim it collects allocation results belonging to this driver, records
// them in an in-memory map, and returns device mappings to the kubelet.
func (d *Driver) PrepareResourceClaims(
	ctx context.Context,
	claims []*resourceapi.ResourceClaim,
) (map[types.UID]kubeletplugin.PrepareResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	results := make(map[types.UID]kubeletplugin.PrepareResult, len(claims))
	for _, claim := range claims {
		// Idempotency check: if already prepared, return cached result.
		if pc, ok := d.prepared[claim.UID]; ok {
			klog.V(2).InfoS("claim already prepared, returning cached result",
				"claimUID", claim.UID)
			results[claim.UID] = buildPrepareResult(pc)
			continue
		}

		// Validate allocation exists.
		if claim.Status.Allocation == nil {
			results[claim.UID] = kubeletplugin.PrepareResult{
				Err: fmt.Errorf("claim %s/%s has no allocation", claim.Namespace, claim.Name),
			}
			continue
		}

		// Collect allocation results for this driver.
		var devices []PreparedDevice
		for _, result := range claim.Status.Allocation.Devices.Results {
			if result.Driver != d.driverName {
				continue
			}
			pd := PreparedDevice{
				Request: result.Request,
				Pool:    result.Pool,
				Device:  result.Device,
			}
			if result.ShareID != nil {
				pd.ShareID = string(*result.ShareID)
			}
			if result.ConsumedCapacity != nil {
				if shares, ok := result.ConsumedCapacity["shares"]; ok {
					pd.ConsumedCapacity = shares.Value()
				}
			}
			devices = append(devices, pd)
		}

		// No results for our driver is an error.
		if len(devices) == 0 {
			results[claim.UID] = kubeletplugin.PrepareResult{
				Err: fmt.Errorf("claim %s/%s has no allocation results for driver %s",
					claim.Namespace, claim.Name, d.driverName),
			}
			continue
		}

		// Record the prepared claim.
		pc := &PreparedClaim{
			ClaimUID:  claim.UID,
			Namespace: claim.Namespace,
			Name:      claim.Name,
			Devices:   devices,
		}
		d.prepared[claim.UID] = pc

		klog.InfoS("preparing claim",
			"claimUID", claim.UID,
			"namespace", claim.Namespace,
			"name", claim.Name,
			"allocationResults", len(devices),
			"totalShares", pc.TotalShares())

		results[claim.UID] = buildPrepareResult(pc)
	}
	return results, nil
}

// buildPrepareResult converts a PreparedClaim into the kubeletplugin.PrepareResult
// that the kubelet expects.
func buildPrepareResult(pc *PreparedClaim) kubeletplugin.PrepareResult {
	kDevices := make([]kubeletplugin.Device, len(pc.Devices))
	for i, pd := range pc.Devices {
		kDevices[i] = kubeletplugin.Device{
			Requests:   []string{pd.Request},
			PoolName:   pd.Pool,
			DeviceName: pd.Device,
		}
	}
	return kubeletplugin.PrepareResult{Devices: kDevices}
}

// UnprepareResourceClaims removes prepared claims from the in-memory map.
// This is idempotent: unpreparing an unknown claim is a no-op.
func (d *Driver) UnprepareResourceClaims(
	ctx context.Context,
	claims []kubeletplugin.NamespacedObject,
) (map[types.UID]error, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	results := make(map[types.UID]error, len(claims))
	for _, claim := range claims {
		klog.InfoS("unpreparing claim", "claimUID", claim.UID)
		delete(d.prepared, claim.UID)
		results[claim.UID] = nil
	}
	return results, nil
}

// HandleError handles errors from the kubelet plugin helper.
// Recoverable errors are logged; fatal errors cause shutdown.
func (d *Driver) HandleError(ctx context.Context, err error, msg string) {
	if errors.Is(err, kubeletplugin.ErrRecoverable) {
		klog.ErrorS(err, "recoverable plugin error", "msg", msg)
		return
	}
	klog.ErrorS(err, "fatal plugin error, shutting down", "msg", msg)
	d.cancelFunc()
}
