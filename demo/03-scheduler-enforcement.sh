#!/usr/bin/env bash
# Demo 3: Scheduler Rejects Pods When Capacity Exhausted
#
# Shows that a greedy pod requesting 960 shares stays Pending when only
# 950 remain, then schedules after freeing shares. Same UX as requesting
# an unavailable GPU.
#
# Prerequisites: kind cluster deployed per TESTING.md, Demo 2 pod running
set -euo pipefail

NS="trace-dra-test"
CTX="kind-trace-dra-test"

echo "# ── Demo 3: Scheduler Rejects Pods When Capacity Exhausted ──"
echo ""

echo "# The extended-resource pod is using 50 shares (5 units x 10 shares/unit)."
echo "# That leaves 950 of 1000 shares free."
echo "# A greedy pod wants 960 shares — more than available:"
cat example/greedy-claim.yaml
echo ""
sleep 2

echo "# Deploy the greedy claim + pod:"
kubectl --context "$CTX" -n "$NS" apply -f example/greedy-claim.yaml
echo ""
sleep 3

echo "# The greedy pod is Pending — not enough capacity:"
kubectl --context "$CTX" -n "$NS" get pod greedy-pod
echo ""
sleep 2

echo "# Scheduler events confirm insufficient capacity:"
kubectl --context "$CTX" -n "$NS" describe pod greedy-pod | tail -5
echo ""
sleep 2

echo "# Free up shares by deleting the extended-resource pod:"
kubectl --context "$CTX" -n "$NS" delete pod trace-consumer-extended
echo ""
sleep 2

echo "# Watch the greedy pod go from Pending → Running:"
kubectl --context "$CTX" -n "$NS" get pod greedy-pod -w &
WATCH_PID=$!
# Give the scheduler time to react, then stop the watch
sleep 15
kill $WATCH_PID 2>/dev/null || true
echo ""

echo "# Cleanup:"
kubectl --context "$CTX" -n "$NS" delete -f example/greedy-claim.yaml --ignore-not-found
