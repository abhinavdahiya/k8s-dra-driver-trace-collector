package driver

import (
	"strconv"

	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/dynamic-resource-allocation/resourceslice"
	"k8s.io/utils/ptr"
)

// BuildResources returns a DriverResources describing the single
// trace-capacity device for this node. The device uses consumable
// capacity with AllowMultipleAllocations so the scheduler can
// allocate shares from the same device to multiple claims.
func (d *Driver) BuildResources() resourceslice.DriverResources {
	device := resourceapi.Device{
		Name: "trace-capacity",
		Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
			"type": {StringValue: ptr.To("trace-capacity")},
		},
		Capacity: map[resourceapi.QualifiedName]resourceapi.DeviceCapacity{
			"shares": {
				Value: resource.MustParse(strconv.Itoa(d.cfg.Driver.TotalShares)),
				RequestPolicy: &resourceapi.CapacityRequestPolicy{
					Default: ptr.To(resource.MustParse("10")),
					ValidRange: &resourceapi.CapacityRequestPolicyRange{
						Min:  ptr.To(resource.MustParse("10")),
						Step: ptr.To(resource.MustParse("10")),
					},
				},
			},
		},
		AllowMultipleAllocations: ptr.To(true),
	}

	return resourceslice.DriverResources{
		Pools: map[string]resourceslice.Pool{
			d.nodeName: {
				Slices: []resourceslice.Slice{{
					Devices: []resourceapi.Device{device},
				}},
			},
		},
	}
}
