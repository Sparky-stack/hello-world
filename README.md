# hello-world

Minimal Go HTTP service used to exercise the `koorikla/platform` stack (Cluster API + Argo CD + OpenChoreo 1.3.3) end to end.

## Endpoints

- `GET /` — returns JSON `{"message", "hostname", "version"}`
- `GET /healthz` — returns `200 OK` with `{"status": "ok"}`

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

Configured in `.github/workflows/ci.yaml`:

- **Pull requests to `main`**: runs `go vet ./...`, `go test -race -v ./...`, `hack/validate-manifests.py`, and a local `docker build` (without pushing).
- **Push to `main`** (ignoring `deploy/**` changes):
  1. `test`: runs Go vet, Go race tests, and OpenChoreo manifest validation.
  2. `build`: builds the multi-stage distroless image (`VERSION=<short-sha>`) and pushes `ghcr.io/koorikla/hello-world:<short-sha>` and `:main` to GHCR using `GITHUB_TOKEN` (`packages: write`).
  3. `bump`: updates `deploy/openchoreo/workload.yaml` (`spec.container.image` and `spec.source.commit`), re-validates manifests, and commits back to `main` as `github-actions[bot]` with `[skip ci]`.

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
