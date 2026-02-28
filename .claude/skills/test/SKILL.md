---
name: test
description: Run tests for the trace DRA driver. Local checks and kind cluster integration tests.
---

# Test Skill

## Local Checks

Always run these first:

```bash
go build ./...
go vet ./...
go test ./...
```

Report pass/fail for each. Stop on failure unless the user asks
to continue.

## Integration Tests (kind cluster)

### Context and Namespace Safety

kubectl write operations (apply, create, delete, etc.) are
permitted ONLY when ALL of these are true:

1. The `--context` flag explicitly targets a `kind-*` context.
2. The `-n` flag explicitly targets the `trace-dra-test` namespace
   (or the resource is cluster-scoped like DeviceClass/ResourceSlice).

Before any kubectl write:

1. Run `kubectl config get-contexts` to find contexts matching
   `kind-*`.
2. If no kind context exists, show the user the cluster creation
   command from TESTING.md and stop. Do not proceed without a
   kind context.
3. If `kind-trace-dra-test` exists, use it.
4. If multiple kind contexts exist but `kind-trace-dra-test` is
   not among them, ask the user which one to use.
5. Pass `--context kind-<name> -n trace-dra-test` on EVERY
   kubectl command.

Never run kubectl write operations against a non-kind context
or a namespace other than `trace-dra-test`.

### Deploy

```bash
kubectl --context kind-trace-dra-test create namespace trace-dra-test
KO_DOCKER_REPO=ko.local ko apply --context kind-trace-dra-test -f deploy/
```

### Verify

```bash
kubectl --context kind-trace-dra-test -n trace-dra-test get pods -l app=trace-collector
kubectl --context kind-trace-dra-test get resourceslices
```

### Reference

See TESTING.md for the full manual procedure.
