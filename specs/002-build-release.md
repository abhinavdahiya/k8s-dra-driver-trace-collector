# Build, Release, and CI/CD

Container image builds, snapshot publishing, and release automation
for the trace DRA driver using ko, GitHub Actions, and GitHub
Container Registry (GHCR).

---

## Table of Contents

- [Problem Statement](#problem-statement)
- [Scope](#scope)
- [Design Decisions](#design-decisions)
  - [Why ko (no Dockerfile, no Goreleaser)](#why-ko-no-dockerfile-no-goreleaser)
  - [Why GHCR](#why-ghcr)
  - [Why No Makefile](#why-no-makefile)
- [Image References](#image-references)
- [Versioning](#versioning)
- [ko Configuration](#ko-configuration)
- [CI Workflow](#ci-workflow)
- [Snapshot Workflow](#snapshot-workflow)
- [Release Workflow](#release-workflow)
  - [Release Process](#release-process)
  - [Resolved Manifests](#resolved-manifests)
- [Repository Configuration](#repository-configuration)
- [Future Work](#future-work)

---

## Problem Statement

The project has no CI, no container image registry, and no release
process. Contributors cannot verify changes without a local kind
cluster, and consumers have no published images to deploy.

The driver needs:

1. **Automated checks** on every pull request and push to `main`
   (vet, test, build verification).
2. **Snapshot images** on every push to `main`, so developers can
   test the latest code in a cluster without building locally.
3. **Tagged release images** with corresponding GitHub Releases and
   pre-resolved Kubernetes manifests, so consumers can deploy a
   specific version with `kubectl apply -f`.

## Scope

- **CI, image publishing, and release automation only.** This spec
  does not cover integration testing in CI (running a kind cluster
  in GitHub Actions), image signing (cosign/Sigstore), or SBOM
  attestation. Those are future work.
- **GitHub-native.** All tooling uses GitHub Actions, GitHub
  Container Registry, and GitHub Releases. No external CI services
  or registries.

---

## Design Decisions

### Why ko (no Dockerfile, no Goreleaser)

The project already uses [ko](https://ko.build/) for local image
builds. ko is purpose-built for Go + Kubernetes:

- Compiles the Go binary and packages it into a distroless base
  image in one step. No Dockerfile to maintain.
- Produces multi-arch images (`linux/amd64`, `linux/arm64`) from a
  single command.
- Resolves `ko://` image references in Kubernetes YAML, producing
  deployment manifests with digested image references baked in.
- Used by upstream Kubernetes projects (e.g. knative, tekton,
  sigstore).

Goreleaser is unnecessary because the project ships a container
image, not standalone binaries. ko handles image building, tagging,
and pushing in one tool.

### Why GHCR

GitHub Container Registry (`ghcr.io`) is the natural choice:

- Free for public repositories.
- Authenticates with the same `GITHUB_TOKEN` used by Actions -- no
  external credentials to manage.
- Images appear in the repository's "Packages" tab, linked to the
  source code.
- Supports OCI image indexes (multi-arch manifests).

Image path: `ghcr.io/abhinavdahiya/k8s-dra-driver-trace-collector`.

### Why No Makefile

The project has three build commands:

```bash
go vet ./...
go test -race ./...
ko build ./
```

A Makefile adds indirection without reducing complexity. Developers
run the commands directly; CI workflows spell them out explicitly.
If the project grows to need generated code, linting tools, or
multi-step build pipelines, a Makefile or Taskfile can be added at
that point.

---

## Image References

| Scenario | Image Tag | Example |
|---|---|---|
| Latest from `main` | `latest` | `ghcr.io/abhinavdahiya/k8s-dra-driver-trace-collector:latest` |
| Specific commit on `main` | `main-<7-char-sha>` | `ghcr.io/abhinavdahiya/k8s-dra-driver-trace-collector:main-abc1234` |
| Tagged release | `v<semver>` | `ghcr.io/abhinavdahiya/k8s-dra-driver-trace-collector:v0.1.0` |
| Local development (ko) | `<digest>` | `ko.local/k8s-dra-driver-trace-collector:latest` |

Snapshot tags (`main-<sha>`) are pinnable and immutable -- a given
SHA always produces the same image contents. The `latest` tag is a
rolling pointer to the most recent build on `main` (for snapshots)
or the most recent release (for tagged builds).

---

## Versioning

Semantic versioning via git tags: `v0.1.0`, `v0.2.0`, `v1.0.0`.

The version is the git tag itself. There is no version variable
embedded in the binary via `-ldflags` at this stage. The image tag
serves as the version identifier. Binary version embedding can be
added later if the driver needs to report its version at runtime
(e.g. for logging or health endpoints).

The first release should be `v0.1.0` to signal early/pre-stable
status. Once the driver reaches Milestone 3 (checkpoint + crash
recovery), it can be tagged `v0.2.0` or `v0.3.0`. The `v1.0.0`
release is reserved for when the API and behavior are considered
stable.

---

## ko Configuration

Update `.ko.yaml` to declare default platforms:

```yaml
defaultBaseImage: gcr.io/distroless/static:latest
defaultPlatforms:
  - linux/amd64
  - linux/arm64
```

This ensures all `ko build` invocations (local and CI) produce
multi-arch images by default. Developers on Apple Silicon can run
the `linux/arm64` variant locally without cross-compilation flags.

---

## CI Workflow

**File:** `.github/workflows/ci.yaml`

**Triggers:** push to `main`, pull requests targeting `main`.

**Purpose:** Fast feedback on every change. Does not push images.

### Jobs

**test** -- runs on `ubuntu-latest`:

1. Check out code.
2. Set up Go (version from `go.mod`).
3. Run `go vet ./...`.
4. Run `go test -race ./...`.

**build** -- runs on `ubuntu-latest`, in parallel with `test`:

1. Check out code.
2. Set up Go (version from `go.mod`).
3. Install ko via `ko-build/setup-ko` action.
4. Run `ko build ./` with `KO_DOCKER_REPO=ko.local`.

The build job verifies that the Go binary compiles and that ko can
produce a valid container image. It does not push anywhere.

### Permissions

```yaml
permissions:
  contents: read
```

Read-only. No write access to packages or releases.

---

## Snapshot Workflow

**File:** `.github/workflows/snapshot.yaml`

**Triggers:** push to `main` only (not PRs).

**Purpose:** Publish a snapshot image for every commit that lands on
`main`, so developers and testers can pull the latest code without
building locally.

### Steps

1. Check out code.
2. Set up Go (version from `go.mod`).
3. Install ko via `ko-build/setup-ko` action.
4. Log into GHCR:
   ```bash
   echo "$GITHUB_TOKEN" | ko login ghcr.io -u "$GITHUB_ACTOR" --password-stdin
   ```
5. Build and push multi-arch image:
   ```bash
   SHORT_SHA=$(echo "$GITHUB_SHA" | head -c 7)
   ko build -B -t "main-${SHORT_SHA},latest" ./
   ```
   The `-B` (`--bare`) flag strips the Go import path from the image
   name, producing a clean `ghcr.io/.../k8s-dra-driver-trace-collector`
   path.

### Permissions

```yaml
permissions:
  contents: read
  packages: write
```

`packages: write` is required to push to GHCR.

### Environment

```yaml
env:
  KO_DOCKER_REPO: ghcr.io/abhinavdahiya/k8s-dra-driver-trace-collector
```

---

## Release Workflow

**File:** `.github/workflows/release.yaml`

**Triggers:** push of a tag matching `v*` (e.g. `v0.1.0`).

**Purpose:** Build and push a release image, produce resolved
Kubernetes manifests, and create a GitHub Release.

### Steps

1. Check out code.
2. Set up Go (version from `go.mod`).
3. Install ko via `ko-build/setup-ko` action.
4. Log into GHCR (same as snapshot).
5. Build and push multi-arch image tagged with the version and
   `latest`:
   ```bash
   VERSION="${GITHUB_REF_NAME}"    # e.g. "v0.1.0"
   ko build -B -t "${VERSION},latest" ./
   ```
6. Produce resolved Kubernetes manifests:
   ```bash
   ko resolve -f deploy/ > install.yaml
   ```
   This processes every YAML file in `deploy/`, replacing `ko://`
   image references with the fully-qualified digested image path.
   The output is a single multi-document YAML file that consumers
   can apply directly.
7. Create the GitHub Release with auto-generated notes and the
   resolved manifests as an asset:
   ```bash
   gh release create "${VERSION}" \
     --generate-notes \
     install.yaml
   ```

### Permissions

```yaml
permissions:
  contents: write
  packages: write
```

`contents: write` is required to create GitHub Releases and upload
assets. `packages: write` is required to push to GHCR.

### Release Process

To cut a release:

```bash
git tag v0.1.0
git push origin v0.1.0
```

GitHub Actions handles the rest:

1. CI workflow runs tests (triggered by the tag push).
2. Release workflow builds and pushes the image to GHCR.
3. Release workflow creates a GitHub Release with:
   - Auto-generated release notes (based on PRs and commits since
     the previous tag).
   - `install.yaml` -- resolved Kubernetes manifests ready for
     `kubectl apply -f`.

### Resolved Manifests

The `install.yaml` release asset contains all files from `deploy/`
(`daemonset.yaml`, `rbac.yaml`, `deviceclass.yaml`) concatenated
into a single YAML stream with `ko://` references replaced by
digested image URIs.

Consumers deploy a specific release with:

```bash
# From GitHub Releases
kubectl apply -f https://github.com/abhinavdahiya/k8s-dra-driver-trace-collector/releases/download/v0.1.0/install.yaml
```

This eliminates the need for consumers to install ko or clone the
repository.

---

## Repository Configuration

### Branch Protection

Enable branch protection on `main`:

- Require the `test` and `build` status checks to pass before
  merging.
- Require pull request reviews.
- Do not allow direct pushes to `main`.

### Package Visibility

After the first image is pushed, set the GHCR package visibility
to **public** in the repository's Packages settings. Public
packages do not count against storage quotas and are pullable
without authentication.

---

## Future Work

| Area | Description |
|---|---|
| **Image signing** | Add cosign keyless signing (Sigstore/Fulcio) to the release workflow. Standard practice in the Kubernetes ecosystem for supply-chain security. |
| **SBOM attestation** | ko generates SBOMs by default (`--sbom=spdx`). Attach them as image attestations for `cosign verify-attestation`. |
| **Integration tests in CI** | Run a kind cluster in CI with DRA feature gates enabled, deploy the driver, exercise the example workloads. Requires a dedicated workflow with `kind` and `kubectl`. |
| **Binary version embedding** | Inject the git tag into `main.version` via `-ldflags` so the driver can log its version at startup. |
| **Dependabot / Renovate** | Automate Go module and GitHub Actions dependency updates. |
| **Release branches** | If the project needs to maintain multiple release lines (e.g. `release-0.1`, `release-0.2`), add branch-based release automation. Not needed until post-v1.0. |
