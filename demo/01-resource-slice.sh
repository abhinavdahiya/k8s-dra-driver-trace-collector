#!/usr/bin/env bash
# Demo 1: Trace Capacity as a Schedulable Resource
#
# Shows that the DRA driver publishes a ResourceSlice with 1000
# consumable shares — just like a GPU, but for trace processing.
#
# Prerequisites: kind cluster deployed per TESTING.md
set -euo pipefail

NS="trace-dra-test"
CTX="kind-trace-dra-test"

echo "# ── Demo 1: Trace Capacity as a Schedulable Resource ──"
echo ""

echo "# The trace-collector pods run with the Alloy sidecar (2/2 READY):"
kubectl --context "$CTX" -n "$NS" get pods -l app=trace-collector -o wide
echo ""
sleep 2

echo "# The DRA driver publishes a ResourceSlice — one per node:"
kubectl --context "$CTX" get resourceslices -o wide
echo ""
sleep 2

echo "# The slice advertises a 'trace-capacity' device with 1000 shares:"
kubectl --context "$CTX" get resourceslices -o yaml | head -40
