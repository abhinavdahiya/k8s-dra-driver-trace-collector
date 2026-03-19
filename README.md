# k8s-dra-driver-trace-collector

> **Experimental.** This project is a proof-of-concept and is not intended for production use.

Kubernetes [Dynamic Resource Allocation](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/) (DRA) kubelet plugin written in Go that models per-node trace processing capacity as a schedulable resource.

Just like a GPU DRA driver publishes `nvidia.com/gpu` devices, this driver publishes `trace.example.com/capacity` — turning trace collector throughput into something the scheduler understands. Pods request trace shares the same way they request GPUs, and the scheduler enforces capacity limits so no collector is overloaded.

## Demos

> Recorded against a [kind](https://kind.sigs.k8s.io/) cluster. See [TESTING.md](TESTING.md) for setup instructions, or run the scripts in [`demo/`](demo/) to reproduce.

### Demo 1: Trace Capacity as a Schedulable Resource

The DRA driver publishes a `ResourceSlice` with 1000 consumable shares per node — just like how GPU drivers publish devices.

[![asciicast](https://asciinema.org/a/hpS7aGEomUOp2OVD.svg)](https://asciinema.org/a/hpS7aGEomUOp2OVD)

<details>
<summary>Commands</summary>

```bash
kubectl -n trace-dra-test get pods -l app=trace-collector -o wide   # 2/2 READY
kubectl get resourceslices -o wide                                   # driver: trace.example.com
kubectl get resourceslices -o yaml | head -40                        # capacity.shares: 1000
```

</details>

### Demo 2: Extended Resource Syntax — No ResourceClaim Needed

A pod requests trace capacity with `resources.requests: { trace.example.com/capacity: "5" }` — no ResourceClaim YAML. The scheduler auto-creates a ResourceClaim, and the pod gets `TRACE_ENDPOINT` injected via [CDI](https://github.com/cncf-tags/container-device-interface). As simple as `nvidia.com/gpu: 1`.

[![asciicast](https://asciinema.org/a/BaDcfQpahr5OjPY1.svg)](https://asciinema.org/a/BaDcfQpahr5OjPY1)

<details>
<summary>Commands</summary>

```bash
cat example/extended-resource.yaml                                   # just resources.requests
kubectl -n trace-dra-test apply -f example/extended-resource.yaml
kubectl -n trace-dra-test get resourceclaims                         # auto-created claim
kubectl -n trace-dra-test exec trace-consumer-extended -- env | grep TRACE_ENDPOINT
```

</details>

### Demo 3: Scheduler Rejects Pods When Capacity Exhausted

A greedy pod requesting 960 shares stays Pending when only 950 remain. After freeing shares, it immediately schedules. Same UX as requesting an unavailable GPU.

[![asciicast](https://asciinema.org/a/tuSuY374MBFRLtjQ.svg)](https://asciinema.org/a/tuSuY374MBFRLtjQ)

<details>
<summary>Commands</summary>

```bash
kubectl -n trace-dra-test apply -f example/greedy-claim.yaml         # 960-share claim + pod
kubectl -n trace-dra-test get pod greedy-pod                         # Pending
kubectl -n trace-dra-test describe pod greedy-pod | tail -5          # insufficient capacity
# Free shares
kubectl -n trace-dra-test delete pod trace-consumer-extended
# Greedy pod now schedules
kubectl -n trace-dra-test get pod greedy-pod -w                      # Pending -> Running
```

</details>

## Quick Start

```bash
# Create a kind cluster with DRA feature gates
cat <<EOF | kind create cluster --name trace-dra-test --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
featureGates:
  DynamicResourceAllocation: true
  DRAConsumableCapacity: true
  DRAExtendedResource: true
EOF

kubectl --context kind-trace-dra-test create namespace trace-dra-test

# Build, load, and deploy
KO_DOCKER_REPO=ko.local ko resolve -f deploy/ > /tmp/ko-resolved.yaml
IMAGE=$(grep 'image:.*ko.local' /tmp/ko-resolved.yaml | awk '{print $2}')
kind load docker-image --name trace-dra-test "$IMAGE"
kubectl --context kind-trace-dra-test apply -f /tmp/ko-resolved.yaml

# Verify
kubectl --context kind-trace-dra-test -n trace-dra-test get pods -l app=trace-collector
kubectl --context kind-trace-dra-test get resourceslices
```

See [TESTING.md](TESTING.md) for full details.
