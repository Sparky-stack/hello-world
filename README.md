# hello-world

Minimal Go HTTP service used to exercise the `koorikla/platform` stack (Cluster API + Argo CD + OpenChoreo 1.3.3) end to end.

## Endpoints

- `GET /` — returns JSON `{"message", "hostname", "version"}`
- `GET /healthz` — returns `200 OK` with `{"status": "ok"}`
- `GET /version` — returns `200 OK` with `{"version": "<git sha>"}`

Configuration:

- `PORT` (default `8080`)
- `GREETING` (default `hello`)
- `main.version` (injected at build time via `-ldflags "-X main.version=<git sha>"`, default `dev`)

## Local run & verification

```bash
mise exec -- go test -race -v ./...
mise exec -- go run .
```

Validate the OpenChoreo 1.3.3 manifests against the vendored CRD schemas:

```bash
uv run --with pyyaml --with jsonschema python3 hack/validate-manifests.py
```

Build the container image locally:

```bash
docker build --build-arg VERSION=dev -t hello-world:dev .
```

## Build flow

**Default: OpenChoreo workflow plane.** `deploy/openchoreo/component.yaml` names the platform's ClusterWorkflow `go`.
A WorkflowRun (Backstage → hello-world → **Build**, or a push once the GitHub webhook reaches openchoreo-api) runs on
the platform's workflow plane, with the same [koorikla/ci](https://github.com/koorikla/ci) scripts as the GitHub path:

1. checkout → `scripts/go/check.sh` (`go vet`, golangci-lint if present, `go test -race`, govulncheck) and a Trivy
   lockfile scan;
2. rootless BuildKit builds `linux/amd64` + `linux/arm64` and pushes
   `harbor.openchoreo.localhost:9443/apps/demo/hello-world:<short-sha>` (platform registry) with SLSA provenance;
3. Trivy scan per platform (fails on CRITICAL), syft SBOM, cosign signature with the platform key + vuln and SBOM
   attestations;
4. commits `spec.container.image` (`<repo>:<short-sha>@sha256:…`) and `spec.source.commit` into
   `deploy/openchoreo/workload.yaml` with `[skip ci]`; Argo CD syncs it and `autoDeploy` releases it to development.

**GitHub Actions** (`.github/workflows/ci.yaml`, reusable
[`koorikla/ci/.github/workflows/go.yaml`](https://github.com/koorikla/ci)) always validates the manifests and runs
the Go checks (CodeQL included). With repository variable `IMAGE_BUILDER=github-actions` it is the build path instead:
multi-arch image to `ghcr.io/koorikla/hello-world:<short-sha>`, Trivy, SBOM, GitHub SLSA provenance, cosign keyless
signature, and job `bump` commits the image ref. Use one path at a time (both write `workload.yaml`).

Verify a platform-built image (default path; platform key, no public Rekor entry by default):

```sh
# cosign.pub of the platform (public): from the platform operator, or on the mgmt cluster
kubectl -n platform-secrets get secret cosign-platform -o jsonpath='{.data.cosign\.pub}' | base64 -d > cosign.pub
# Harbor uses the platform's registry CA: trust it for cosign (SSL_CERT_FILE) or `docker login` first
cosign verify --key cosign.pub --insecure-ignore-tlog=true \
  harbor.openchoreo.localhost:9443/apps/demo/hello-world@sha256:<digest>
cosign verify-attestation --key cosign.pub --insecure-ignore-tlog=true --type https://spdx.dev/Document/v2.3 \
  harbor.openchoreo.localhost:9443/apps/demo/hello-world@sha256:<digest>
```

`--insecure-ignore-tlog=true` because the platform signs without uploading to the public Rekor log (installation
switch `buildWorkflows.signing.transparencyLog`); drop it when that switch is on. The digest is the one in
`deploy/openchoreo/workload.yaml`.

Verify a GitHub-built image (keyless):

```sh
cosign verify ghcr.io/koorikla/hello-world:<short-sha> \
  --certificate-identity-regexp '^https://github.com/koorikla/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## Deployment & Promotion

`deploy/openchoreo/` holds the OpenChoreo 1.3.3 developer manifests for project `demo`:

- `component.yaml`: `Component/hello-world` referencing `ClusterComponentType` `deployment/service` with `autoDeploy: true`.
- `workload.yaml`: `Workload/hello-world-workload` defining the GHCR image, `GREETING` env, and external `HTTP` endpoint on port `8080`.
- `release-bindings/development.yaml`: `ReleaseBinding/hello-world-development` (omits `spec.releaseName`; `autoDeploy: true` causes the OpenChoreo controller to cut an immutable `ComponentRelease` named `hello-world-<hash>` and update this binding automatically, while Argo CD ignores `/spec/releaseName`).
- `release-bindings/staging.yaml` & `production.yaml`: `ReleaseBinding` objects for `staging` (on `dev1`) and `production` (on `prod1`), dormant until promoted.

### Promotion

To promote to `staging` or `production`, open a PR setting `spec.releaseName` in `deploy/openchoreo/release-bindings/staging.yaml` (or `production.yaml`) to the `ComponentRelease` name (`hello-world-<hash>`) currently running in the preceding environment.

### Platform-side registration

The platform repository (`koorikla/platform`) registers this app in `installations/local/tenants/demo.yaml`, which creates the `demo` OpenChoreo `Project`, an isolated `AppProject` (`demo`) whitelisting `Component`, `Workload`, and `ReleaseBinding` in namespace `default`, and an Argo CD `Application` syncing `deploy/openchoreo`.
