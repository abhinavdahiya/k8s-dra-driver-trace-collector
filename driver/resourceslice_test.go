package driver

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/abhinavdahiya/k8s-dra-driver-trace-collector/driver/config"
)

func newTestDriverForSlice(t *testing.T, nodeName string, totalShares int) *Driver {
	t.Helper()
	cfg := &config.TraceDRADriverConfiguration{
		TypeMeta: metav1.TypeMeta{
			APIVersion: config.APIVersion,
			Kind:       config.Kind,
		},
		Driver: config.DriverSpec{
			Name:        "trace.example.com",
			TotalShares: totalShares,
			StepSize:    10,
		},
		Alloy: config.AlloySpec{
			Address:            "http://127.0.0.1:12345",
			ConfigDir:          t.TempDir(),
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
		Reconciler: config.ReconcilerSpec{
			Interval: metav1.Duration{Duration: 30 * time.Second},
		},
	}
	return New(DriverOptions{
		NodeName:   nodeName,
		CDIDir:     t.TempDir(),
		Config:     cfg,
		CancelFunc: func() {},
	})
}

func TestBuildResources_Default(t *testing.T) {
	drv := newTestDriverForSlice(t, "node-1", 1000)
	res := drv.BuildResources()

	require.Len(t, res.Pools, 1)
	pool, ok := res.Pools["node-1"]
	require.True(t, ok, "expected pool named 'node-1'")
	require.Len(t, pool.Slices, 1)
	require.Len(t, pool.Slices[0].Devices, 1)
	assert.Equal(t, "trace-capacity", pool.Slices[0].Devices[0].Name)
}

func TestBuildResources_AllowMultipleAllocations(t *testing.T) {
	drv := newTestDriverForSlice(t, "node-1", 1000)
	res := drv.BuildResources()

	device := res.Pools["node-1"].Slices[0].Devices[0]
	require.NotNil(t, device.AllowMultipleAllocations)
	assert.True(t, *device.AllowMultipleAllocations)
}

func TestBuildResources_Capacity(t *testing.T) {
	drv := newTestDriverForSlice(t, "node-1", 1000)
	res := drv.BuildResources()

	device := res.Pools["node-1"].Slices[0].Devices[0]
	sharesCap, ok := device.Capacity[resourceapi.QualifiedName("shares")]
	require.True(t, ok, "expected capacity entry for 'shares'")

	expected := resource.MustParse("1000")
	assert.True(t, sharesCap.Value.Equal(expected), "expected capacity %s, got %s", expected.String(), sharesCap.Value.String())
}

func TestBuildResources_RequestPolicy(t *testing.T) {
	drv := newTestDriverForSlice(t, "node-1", 1000)
	res := drv.BuildResources()

	device := res.Pools["node-1"].Slices[0].Devices[0]
	sharesCap := device.Capacity[resourceapi.QualifiedName("shares")]

	require.NotNil(t, sharesCap.RequestPolicy)
	require.NotNil(t, sharesCap.RequestPolicy.Default)

	expectedDefault := resource.MustParse("10")
	assert.True(t, sharesCap.RequestPolicy.Default.Equal(expectedDefault),
		"expected default %s, got %s", expectedDefault.String(), sharesCap.RequestPolicy.Default.String())

	require.NotNil(t, sharesCap.RequestPolicy.ValidRange)
	require.NotNil(t, sharesCap.RequestPolicy.ValidRange.Min)

	expectedMin := resource.MustParse("10")
	assert.True(t, sharesCap.RequestPolicy.ValidRange.Min.Equal(expectedMin),
		"expected min %s, got %s", expectedMin.String(), sharesCap.RequestPolicy.ValidRange.Min.String())

	require.NotNil(t, sharesCap.RequestPolicy.ValidRange.Step)
	expectedStep := resource.MustParse("10")
	assert.True(t, sharesCap.RequestPolicy.ValidRange.Step.Equal(expectedStep),
		"expected step %s, got %s", expectedStep.String(), sharesCap.RequestPolicy.ValidRange.Step.String())
}

func TestBuildResources_CustomShares(t *testing.T) {
	drv := newTestDriverForSlice(t, "node-1", 500)
	res := drv.BuildResources()

	device := res.Pools["node-1"].Slices[0].Devices[0]
	sharesCap := device.Capacity[resourceapi.QualifiedName("shares")]

	expected := resource.MustParse("500")
	assert.True(t, sharesCap.Value.Equal(expected), "expected capacity %s, got %s", expected.String(), sharesCap.Value.String())
}

func TestBuildResources_Attribute(t *testing.T) {
	drv := newTestDriverForSlice(t, "node-1", 1000)
	res := drv.BuildResources()

	device := res.Pools["node-1"].Slices[0].Devices[0]
	typeAttr, ok := device.Attributes[resourceapi.QualifiedName("type")]
	require.True(t, ok, "expected attribute entry for 'type'")
	require.NotNil(t, typeAttr.StringValue)
	assert.Equal(t, "trace-capacity", *typeAttr.StringValue)
}

func TestBuildResources_PoolNameMatchesNode(t *testing.T) {
	drv := newTestDriverForSlice(t, "worker-42", 1000)
	res := drv.BuildResources()

	assert.Contains(t, res.Pools, "worker-42")
	assert.Len(t, res.Pools, 1)
}
