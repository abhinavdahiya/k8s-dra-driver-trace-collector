# Testing

## Prerequisites

- Go 1.25+
- [ko](https://ko.build/)
- [kind](https://kind.sigs.k8s.io/)
- kubectl

## Local Checks

```bash
go build ./...
go vet ./...
go test ./...
```

## kind Cluster

All resources are deployed to a dedicated `trace-dra-test` namespace
in a dedicated kind cluster context (`kind-trace-dra-test`).

### Create

```bash
cat <<EOF | kind create cluster --name trace-dra-test --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
featureGates:
  DynamicResourceAllocation: true
  DRAConsumableCapacity: true
  DRAExtendedResource: true
EOF

kubectl --context kind-trace-dra-test create namespace trace-dra-test
```

### Deploy

```bash
KO_DOCKER_REPO=ko.local ko apply --context kind-trace-dra-test -f deploy/
```

### Verify

```bash
kubectl --context kind-trace-dra-test -n trace-dra-test get pods -l app=trace-collector
kubectl --context kind-trace-dra-test get resourceslices
```

### Teardown

```bash
kind delete cluster --name trace-dra-test
```
