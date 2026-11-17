# hello-world

Minimal Go HTTP service used to exercise the `koorikla/platform` stack (Cluster API + Argo CD + OpenChoreo 1.3.3) end to end.

## Endpoints

- `GET /` — returns JSON `{"message", "hostname", "version"}`
- `GET /healthz` — returns `200 OK` with `{"status": "ok"}`
- `GET /version` — returns `200 OK` with `{"version": "<git sha>"}` (the short commit SHA of the build; promotion checks compare it per environment)

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

## CI flow

Configured in `.github/workflows/ci.yaml`, which calls the reusable
[`koorikla/ci/.github/workflows/go.yaml@v1`](https://github.com/koorikla/ci):

1. `validate-manifests`: `hack/validate-manifests.py` against the OpenChoreo CRD schemas.
2. `go` (reusable): `go vet`, golangci-lint, `go test -race` with coverage, govulncheck, CodeQL, then a
   multi-arch (`linux/amd64`, `linux/arm64`) image build. On PRs the image is built (amd64) and
   Trivy-scanned only. On push to `main` it is pushed to `ghcr.io/koorikla/hello-world:<short-sha>`
   (+ `:<full-sha>`, `:main`), Trivy-scanned per platform, and gets an SPDX SBOM, GitHub SLSA provenance
   and SBOM attestations, a cosign keyless signature and a cosign vuln attestation.
3. `bump` (push to `main` only, ignoring `deploy/**`): updates `deploy/openchoreo/workload.yaml`
   (`spec.container.image`, `spec.source.commit`), re-validates, commits back with `[skip ci]`.

Verify an image:

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
