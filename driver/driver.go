// Package driver implements the DRA kubelet plugin for trace capacity management.
package driver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"
	"k8s.io/klog/v2"

	"github.com/abhinavdahiya/k8s-dra-driver-trace-collector/driver/alloy"
	"github.com/abhinavdahiya/k8s-dra-driver-trace-collector/driver/cdi"
	"github.com/abhinavdahiya/k8s-dra-driver-trace-collector/driver/config"
)

// reconcileKey is the single key used in the workqueue. All reconcile
// triggers enqueue this key; the workqueue deduplicates naturally.
const reconcileKey = "reconcile"

// AlloyReloader is the interface for triggering Alloy config reloads.
// Implemented by alloy.Client; substituted in tests.
type AlloyReloader interface {
	Reload(ctx context.Context) error
}

// Compile-time interface assertion.
var _ kubeletplugin.DRAPlugin = (*Driver)(nil)

// Driver implements the kubeletplugin.DRAPlugin interface for the
// trace capacity DRA driver. It publishes a single device with
// consumable shares and handles Prepare/Unprepare lifecycle calls.
type Driver struct {
	nodeName       string
	cfg            *config.TraceDRADriverConfiguration
	checkpointPath string
	cancelFunc     context.CancelFunc

	cdiDir string

	// Workqueue for trigger-based reconciliation.
	// Prepare/Unprepare enqueue reconcileKey; the workqueue deduplicates.
	queue    workqueue.TypedInterface[string]
	reloader AlloyReloader

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

	Listener *ListenerState `json:"listener,omitempty"`
}

// ListenerState records the per-pod listener resources created for
// a prepared claim. Used for reconciliation and cleanup.
type ListenerState struct {
	SocketPath     string `json:"socketPath"`
	ConfigFile     string `json:"configFile"`
	SpansPerSecond int    `json:"spansPerSecond"`
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

// DriverOptions configures a new Driver instance.
type DriverOptions struct {
	NodeName   string
	CDIDir     string // default: /var/run/cdi
	Config     *config.TraceDRADriverConfiguration
	CancelFunc context.CancelFunc
	Reloader   AlloyReloader // Alloy reload client; nil disables reloads
}

// New creates a new Driver instance from the given options.
func New(opts DriverOptions) *Driver {
	cfg := opts.Config

	cdiDir := opts.CDIDir
	if cdiDir == "" {
		cdiDir = "/var/run/cdi"
	}

	return &Driver{
		nodeName:       opts.NodeName,
		cfg:            cfg,
		checkpointPath: filepath.Join(cfg.Driver.CheckpointDir, checkpointFileName),
		cancelFunc:     opts.CancelFunc,
		cdiDir:         cdiDir,
		queue:          workqueue.NewTyped[string](),
		reloader:       opts.Reloader,
		prepared:       make(map[types.UID]*PreparedClaim),
	}
}

// triggerReconcile enqueues a reconcile request. The workqueue
// deduplicates: if reconcileKey is already queued, this is a no-op.
func (d *Driver) triggerReconcile() {
	d.queue.Add(reconcileKey)
}

// PrepareResourceClaims handles the claim preparation lifecycle. For each
// claim it collects allocation results belonging to this driver, writes
// a CDI spec (synchronously), records state, and triggers the reconciler
// to create the Alloy config asynchronously.
func (d *Driver) PrepareResourceClaims(
	ctx context.Context,
	claims []*resourceapi.ResourceClaim,
) (map[types.UID]kubeletplugin.PrepareResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	results := make(map[types.UID]kubeletplugin.PrepareResult, len(claims))
	needsReconcile := false

	for _, claim := range claims {
		claimUID := string(claim.UID)

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
			if result.Driver != d.cfg.Driver.Name {
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
					claim.Namespace, claim.Name, d.cfg.Driver.Name),
			}
			continue
		}

		// Build the prepared claim.
		pc := &PreparedClaim{
			ClaimUID:  claim.UID,
			Namespace: claim.Namespace,
			Name:      claim.Name,
			Devices:   devices,
		}

		// Compute scaled parameters for the listener.
		params := alloy.ComputeParams(
			claimUID,
			pc.TotalShares(),
			d.cfg,
		)

		// Write CDI spec synchronously (kubelet needs it immediately).
		if err := cdi.WriteSpec(d.cdiDir, claimUID, params.SocketPath); err != nil {
			results[claim.UID] = kubeletplugin.PrepareResult{
				Err: fmt.Errorf("writing CDI spec for claim %s: %w", claimUID, err),
			}
			continue
		}

		// Create the per-claim socket directory so the CDI directory
		// bind mount succeeds. Alloy will create the actual socket
		// inside this directory when the listener starts.
		socketDir := filepath.Dir(params.SocketPath)
		if err := os.MkdirAll(socketDir, 0755); err != nil {
			results[claim.UID] = kubeletplugin.PrepareResult{
				Err: fmt.Errorf("creating socket directory for claim %s: %w", claimUID, err),
			}
			continue
		}

		// Record listener state.
		pc.Listener = &ListenerState{
			SocketPath:     params.SocketPath,
			ConfigFile:     alloy.ConfigFileName(claimUID),
			SpansPerSecond: params.SpansPerSecond,
		}

		// Store in prepared map.
		d.prepared[claim.UID] = pc
		needsReconcile = true

		// Save checkpoint (best-effort).
		if err := d.saveCheckpoint(); err != nil {
			klog.ErrorS(err, "checkpoint save failed after prepare", "claimUID", claimUID)
		}

		klog.InfoS("preparing claim",
			"claimUID", claim.UID,
			"namespace", claim.Namespace,
			"name", claim.Name,
			"allocationResults", len(devices),
			"totalShares", pc.TotalShares(),
			"socketPath", params.SocketPath,
			"spansPerSecond", params.SpansPerSecond)

		results[claim.UID] = buildPrepareResult(pc)
	}

	if needsReconcile {
		d.triggerReconcile()
	}

	return results, nil
}

// buildPrepareResult converts a PreparedClaim into the kubeletplugin.PrepareResult
// that the kubelet expects, including CDI device IDs.
func buildPrepareResult(pc *PreparedClaim) kubeletplugin.PrepareResult {
	cdiDeviceID := cdi.DeviceID(string(pc.ClaimUID))
	kDevices := make([]kubeletplugin.Device, len(pc.Devices))
	for i, pd := range pc.Devices {
		kDevices[i] = kubeletplugin.Device{
			Requests:     []string{pd.Request},
			PoolName:     pd.Pool,
			DeviceName:   pd.Device,
			CDIDeviceIDs: []string{cdiDeviceID},
		}
	}
	return kubeletplugin.PrepareResult{Devices: kDevices}
}

// UnprepareResourceClaims removes prepared claims from the in-memory map
// and triggers the reconciler to clean up Alloy configs and CDI specs.
// This is idempotent: unpreparing an unknown claim is a no-op.
func (d *Driver) UnprepareResourceClaims(
	ctx context.Context,
	claims []kubeletplugin.NamespacedObject,
) (map[types.UID]error, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	results := make(map[types.UID]error, len(claims))
	needsReconcile := false

	for _, claim := range claims {
		klog.InfoS("unpreparing claim", "claimUID", claim.UID)
		if _, ok := d.prepared[claim.UID]; ok {
			needsReconcile = true
		}
		delete(d.prepared, claim.UID)
		results[claim.UID] = nil
	}

	// Save checkpoint (best-effort).
	if needsReconcile {
		if err := d.saveCheckpoint(); err != nil {
			klog.ErrorS(err, "checkpoint save failed after unprepare")
		}
	}

	if needsReconcile {
		d.triggerReconcile()
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
