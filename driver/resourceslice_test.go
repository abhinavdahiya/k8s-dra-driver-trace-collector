package driver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestBuildResources_Default(t *testing.T) {
	drv := New("node-1", "trace.example.com", 1000, func() {})
	res := drv.BuildResources()

	require.Len(t, res.Pools, 1)
	pool, ok := res.Pools["node-1"]
	require.True(t, ok, "expected pool named 'node-1'")
	require.Len(t, pool.Slices, 1)
	require.Len(t, pool.Slices[0].Devices, 1)
	assert.Equal(t, "trace-capacity", pool.Slices[0].Devices[0].Name)
}

func TestBuildResources_AllowMultipleAllocations(t *testing.T) {
	drv := New("node-1", "trace.example.com", 1000, func() {})
	res := drv.BuildResources()

	device := res.Pools["node-1"].Slices[0].Devices[0]
	require.NotNil(t, device.AllowMultipleAllocations)
	assert.True(t, *device.AllowMultipleAllocations)
}

func TestBuildResources_Capacity(t *testing.T) {
	drv := New("node-1", "trace.example.com", 1000, func() {})
	res := drv.BuildResources()

	device := res.Pools["node-1"].Slices[0].Devices[0]
	sharesCap, ok := device.Capacity[resourceapi.QualifiedName("shares")]
	require.True(t, ok, "expected capacity entry for 'shares'")

	expected := resource.MustParse("1000")
	assert.True(t, sharesCap.Value.Equal(expected), "expected capacity %s, got %s", expected.String(), sharesCap.Value.String())
}

func TestBuildResources_RequestPolicy(t *testing.T) {
	drv := New("node-1", "trace.example.com", 1000, func() {})
	res := drv.BuildResources()

	device := res.Pools["node-1"].Slices[0].Devices[0]
	sharesCap := device.Capacity[resourceapi.QualifiedName("shares")]

	require.NotNil(t, sharesCap.RequestPolicy)
	require.NotNil(t, sharesCap.RequestPolicy.Default)

	expectedDefault := resource.MustParse("1")
	assert.True(t, sharesCap.RequestPolicy.Default.Equal(expectedDefault),
		"expected default %s, got %s", expectedDefault.String(), sharesCap.RequestPolicy.Default.String())

	require.NotNil(t, sharesCap.RequestPolicy.ValidRange)
	require.NotNil(t, sharesCap.RequestPolicy.ValidRange.Min)

	expectedMin := resource.MustParse("1")
	assert.True(t, sharesCap.RequestPolicy.ValidRange.Min.Equal(expectedMin),
		"expected min %s, got %s", expectedMin.String(), sharesCap.RequestPolicy.ValidRange.Min.String())
}

func TestBuildResources_CustomShares(t *testing.T) {
	drv := New("node-1", "trace.example.com", 500, func() {})
	res := drv.BuildResources()

	device := res.Pools["node-1"].Slices[0].Devices[0]
	sharesCap := device.Capacity[resourceapi.QualifiedName("shares")]

	expected := resource.MustParse("500")
	assert.True(t, sharesCap.Value.Equal(expected), "expected capacity %s, got %s", expected.String(), sharesCap.Value.String())
}

func TestBuildResources_Attribute(t *testing.T) {
	drv := New("node-1", "trace.example.com", 1000, func() {})
	res := drv.BuildResources()

	device := res.Pools["node-1"].Slices[0].Devices[0]
	typeAttr, ok := device.Attributes[resourceapi.QualifiedName("type")]
	require.True(t, ok, "expected attribute entry for 'type'")
	require.NotNil(t, typeAttr.StringValue)
	assert.Equal(t, "trace-capacity", *typeAttr.StringValue)
}

func TestBuildResources_PoolNameMatchesNode(t *testing.T) {
	drv := New("worker-42", "trace.example.com", 1000, func() {})
	res := drv.BuildResources()

	assert.Contains(t, res.Pools, "worker-42")
	assert.Len(t, res.Pools, 1)
}
