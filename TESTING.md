# Testing

## Prerequisites

- Go 1.25+
- [ko](https://ko.build/)
- [kind](https://kind.sigs.k8s.io/)
- kubectl
- A running Docker-compatible daemon (Docker Desktop, Rancher Desktop, colima, etc.)

> **DOCKER_HOST:** If your Docker daemon uses a non-default socket (e.g.
> Rancher Desktop uses `~/.rd/docker.sock`, colima uses
> `~/.colima/default/docker.sock`), you must set `DOCKER_HOST` so that
> `ko` and `kind` can reach it. Run `docker context ls` to find the
> active endpoint, then export it:
>
> ```bash
> # Example for Rancher Desktop:
> export DOCKER_HOST=unix://$HOME/.rd/docker.sock
> ```

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

`ko apply` does not support `--context`, so the deploy is a 3-step
process: build the image, load it into kind, then apply the manifests.

```bash
# 1. Build the image and generate resolved YAML
KO_DOCKER_REPO=ko.local ko resolve -f deploy/ > /tmp/ko-resolved.yaml

# 2. Load the image into kind's containerd
#    (grab the ko.local image reference from the resolved YAML)
IMAGE=$(grep 'image:.*ko.local' /tmp/ko-resolved.yaml | awk '{print $2}')
kind load docker-image --name trace-dra-test "$IMAGE"

# 3. Apply the resolved manifests
kubectl --context kind-trace-dra-test apply -f /tmp/ko-resolved.yaml
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
