// Package driver implements the DRA kubelet plugin for trace capacity management.
package driver

import (
	"context"
	"errors"

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
	nodeName    string
	driverName  string
	totalShares int
	cancelFunc  context.CancelFunc
}

// New creates a new Driver instance.
func New(nodeName, driverName string, totalShares int, cancel context.CancelFunc) *Driver {
	return &Driver{
		nodeName:    nodeName,
		driverName:  driverName,
		totalShares: totalShares,
		cancelFunc:  cancel,
	}
}

// PrepareResourceClaims is a stub in M1. It will be fleshed out in M2.
func (d *Driver) PrepareResourceClaims(
	ctx context.Context,
	claims []*resourceapi.ResourceClaim,
) (map[types.UID]kubeletplugin.PrepareResult, error) {
	results := make(map[types.UID]kubeletplugin.PrepareResult, len(claims))
	for _, claim := range claims {
		klog.InfoS("preparing claim (stub)", "claimUID", claim.UID, "namespace", claim.Namespace, "name", claim.Name)
		results[claim.UID] = kubeletplugin.PrepareResult{}
	}
	return results, nil
}

// UnprepareResourceClaims is a stub in M1. It will be fleshed out in M2.
func (d *Driver) UnprepareResourceClaims(
	ctx context.Context,
	claims []kubeletplugin.NamespacedObject,
) (map[types.UID]error, error) {
	results := make(map[types.UID]error, len(claims))
	for _, claim := range claims {
		klog.InfoS("unpreparing claim (stub)", "claimUID", claim.UID)
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
