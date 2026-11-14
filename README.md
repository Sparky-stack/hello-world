# hello-world

Minimal Go HTTP service deployed on OpenChoreo 1.3.3 (`koorikla/platform`).

## Endpoints

- `GET /` — returns JSON `{"message", "hostname", "version"}`
- `GET /healthz` — returns `200 OK` with `{"status": "ok"}`

Configuration:

- `LISTEN_ADDR` (default `:8080`)
- `GREETING` (default `hello from koorikla`)
- `main.version` (set via `-ldflags "-X main.version=..."`, default `dev`)

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

On every push to `main` (ignoring `deploy/**` changes and commits marked `[skip ci]`):

1. `test`: runs `go vet ./...`, `go test -race -v ./...`, and `hack/validate-manifests.py`.
2. `build`: builds the multi-stage distroless image (`VERSION=<short-sha>`) and pushes `ghcr.io/koorikla/hello-world:<short-sha>` and `:main` to GHCR.
3. `bump`: updates `deploy/openchoreo/workload.yaml` (`spec.container.image` and `spec.source.commit`), re-validates manifests, and commits back to `main` with `[skip ci]`.

## Deployment & Promotion

`deploy/openchoreo/` is synced by Argo CD (registered in `koorikla/platform`):

- `component.yaml`: `Component` referencing `ClusterComponentType` `deployment/service` with `autoDeploy: true`.
- `workload.yaml`: `Workload` defining the container image, default `GREETING`, and external HTTP endpoint on port `8080`.
- `release-bindings/development.yaml`: `ReleaseBinding` for `development` (omits `spec.releaseName` because `autoDeploy: true` lets OpenChoreo create the `ComponentRelease` and update the binding; Argo CD ignores `/spec/releaseName`).
- `release-bindings/staging.yaml` and `production.yaml`: dormant until promoted.

### Promotion

To promote a release to `staging` or `production`, open a PR setting `spec.releaseName` in `deploy/openchoreo/release-bindings/staging.yaml` (or `production.yaml`) to the `ComponentRelease` name currently running in the previous environment.
