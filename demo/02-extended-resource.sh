#!/usr/bin/env bash
# Demo 2: Extended Resource Syntax — No ResourceClaim Needed
#
# Shows that a pod can request trace capacity using the familiar
# resources.requests syntax, just like nvidia.com/gpu: 1. The scheduler
# auto-creates a ResourceClaim, and the pod gets TRACE_ENDPOINT injected
# via CDI.
#
# Prerequisites: kind cluster deployed per TESTING.md
set -euo pipefail

NS="trace-dra-test"
CTX="kind-trace-dra-test"

echo "# ── Demo 2: Extended Resource Syntax ──"
echo ""

echo "# The pod spec uses plain resources.requests — no ResourceClaim YAML:"
cat example/extended-resource.yaml
echo ""
sleep 2

echo "# Deploy it:"
kubectl --context "$CTX" -n "$NS" apply -f example/extended-resource.yaml
echo ""
sleep 3

echo "# The scheduler auto-created a ResourceClaim:"
kubectl --context "$CTX" -n "$NS" get resourceclaims
echo ""
sleep 2

echo "# The pod gets TRACE_ENDPOINT injected automatically via CDI:"
kubectl --context "$CTX" -n "$NS" wait --for=condition=Ready pod/trace-consumer-extended --timeout=30s >/dev/null 2>&1
kubectl --context "$CTX" -n "$NS" exec trace-consumer-extended -- env | grep TRACE_ENDPOINT
