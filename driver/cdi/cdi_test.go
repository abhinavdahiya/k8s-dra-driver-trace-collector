package cdi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	specs "tags.cncf.io/container-device-interface/specs-go"
)

func TestDeviceID(t *testing.T) {
	assert.Equal(t, "trace.example.com/trace=abc-123", DeviceID("abc-123"))
}

func TestNewSpec(t *testing.T) {
	spec := NewSpec("abc-123", "/var/run/alloy/abc-123/claim_abc-123.sock")

	assert.Equal(t, specs.CurrentVersion, spec.Version)
	assert.Equal(t, CDIKind, spec.Kind)
	require.Len(t, spec.Devices, 1)

	dev := spec.Devices[0]
	assert.Equal(t, "abc-123", dev.Name)
	require.Len(t, dev.ContainerEdits.Env, 1)
	assert.Equal(t, "TRACE_ENDPOINT=unix:///var/run/alloy/abc-123/claim_abc-123.sock", dev.ContainerEdits.Env[0])
	require.Len(t, dev.ContainerEdits.Mounts, 1)
	assert.Equal(t, "/var/run/alloy/abc-123", dev.ContainerEdits.Mounts[0].HostPath)
	assert.Equal(t, "/var/run/alloy/abc-123", dev.ContainerEdits.Mounts[0].ContainerPath)
	assert.Empty(t, dev.ContainerEdits.Mounts[0].Type)
	assert.Equal(t, []string{"bind"}, dev.ContainerEdits.Mounts[0].Options)
}

func TestNewSpec_JSONRoundTrip(t *testing.T) {
	spec := NewSpec("abc-123", "/var/run/alloy/abc-123/claim_abc-123.sock")

	data, err := json.MarshalIndent(spec, "", "  ")
	require.NoError(t, err)

	var parsed specs.Spec
	require.NoError(t, json.Unmarshal(data, &parsed))
	assert.Equal(t, *spec, parsed)
}

func TestWriteSpec(t *testing.T) {
	dir := t.TempDir()
	err := WriteSpec(dir, "abc-123", "/var/run/alloy/abc-123/claim_abc-123.sock")
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "trace-abc-123.json"))
	require.NoError(t, err)

	want, err := json.MarshalIndent(NewSpec("abc-123", "/var/run/alloy/abc-123/claim_abc-123.sock"), "", "  ")
	require.NoError(t, err)
	assert.Equal(t, string(want), string(data))
}

func TestWriteSpec_Overwrite(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, WriteSpec(dir, "uid", "/sock1"))
	require.NoError(t, WriteSpec(dir, "uid", "/sock2"))

	data, err := os.ReadFile(filepath.Join(dir, "trace-uid.json"))
	require.NoError(t, err)

	want, err := json.MarshalIndent(NewSpec("uid", "/sock2"), "", "  ")
	require.NoError(t, err)
	assert.Equal(t, string(want), string(data))
}

func TestDeleteSpec(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, WriteSpec(dir, "uid", "/sock"))
	require.NoError(t, DeleteSpec(dir, "uid"))

	_, err := os.Stat(filepath.Join(dir, "trace-uid.json"))
	assert.True(t, os.IsNotExist(err))
}

func TestDeleteSpec_Idempotent(t *testing.T) {
	dir := t.TempDir()
	assert.NoError(t, DeleteSpec(dir, "nonexistent"))
}

func TestWriteSpec_NonexistentDir(t *testing.T) {
	err := WriteSpec("/nonexistent/dir", "uid", "/sock")
	require.Error(t, err)
}
