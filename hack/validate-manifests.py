#!/usr/bin/env python3
"""Validate deploy/openchoreo/ against the OpenChoreo 1.3.3 CRD schemas.

Strict like `kubeconform -strict`: unknown fields are errors (the API server
would silently prune them). Also checks repo invariants (owner refs, secretKeyRef -> SecretReference, binding
names, Workload resource dependency -> Resource -> development ResourceReleaseBinding). Needs: pyyaml, jsonschema.
"""
import json
import pathlib
import sys

import jsonschema
import yaml

ROOT = pathlib.Path(__file__).resolve().parent.parent
SCHEMAS = ROOT / "hack" / "crd-schemas"
MANIFESTS = ROOT / "deploy" / "openchoreo"
PROJECT, COMPONENT = "demo", "hello-world"


def strict(schema):
    """Disallow unknown fields wherever the CRD does not preserve them."""
    if isinstance(schema, dict):
        if (
            schema.get("type") == "object"
            and "properties" in schema
            and "additionalProperties" not in schema
            and not schema.get("x-kubernetes-preserve-unknown-fields")
        ):
            schema["additionalProperties"] = False
        for v in schema.values():
            strict(v)
    elif isinstance(schema, list):
        for v in schema:
            strict(v)
    return schema


def load_schema(kind, api_version):
    group, version = api_version.split("/")
    path = SCHEMAS / f"{kind.lower()}-{group}-{version}.json"
    if not path.exists():
        return None
    schema = strict(json.loads(path.read_text()))
    # metadata is not described by CRD schemas; allow it.
    schema.setdefault("properties", {})["metadata"] = {"type": "object"}
    return schema


def main() -> int:
    errors = []
    seen = {}
    secret_refs = {}  # SecretReference name -> its secretKeys
    secret_key_refs = []  # (where, name, key) of every Workload secretKeyRef
    resources = set()  # Resource names
    resource_refs = []  # (where, name) of every Workload dependencies.resources[].ref
    resource_bindings = []  # (where, resourceName, environment) of every ResourceReleaseBinding
    for path in sorted(MANIFESTS.rglob("*.yaml")):
        rel = path.relative_to(ROOT)
        for i, doc in enumerate(yaml.safe_load_all(path.read_text())):
            if not doc:
                continue
            where = f"{rel}[{i}]"
            kind, api = doc.get("kind"), doc.get("apiVersion", "")
            schema = load_schema(kind, api) if "/" in api else None
            if schema is None:
                errors.append(f"{where}: no schema for {api} {kind}")
                continue
            for e in jsonschema.Draft7Validator(schema).iter_errors(doc):
                loc = ".".join(str(p) for p in e.absolute_path)
                errors.append(f"{where}: {loc}: {e.message}")
            name = doc.get("metadata", {}).get("name")
            if not name:
                errors.append(f"{where}: metadata.name missing")
            if "namespace" in doc.get("metadata", {}):
                errors.append(f"{where}: metadata.namespace must be unset (Argo CD sets it)")
            if (kind, name) in seen:
                errors.append(f"{where}: duplicate {kind}/{name} (also in {seen[(kind, name)]})")
            seen[(kind, name)] = where

            if kind == "SecretReference":  # no owner: org-namespace object, named by the Workload
                secret_refs[name] = {d.get("secretKey") for d in doc["spec"].get("data", [])}
                continue
            if kind == "Workload":
                c = doc["spec"].get("container", {})
                for item in c.get("env", []) + c.get("files", []):
                    ref = (item.get("valueFrom") or {}).get("secretKeyRef")
                    if ref:
                        secret_key_refs.append((where, ref.get("name"), ref.get("key")))
                for dep in doc["spec"].get("dependencies", {}).get("resources", []):
                    resource_refs.append((where, dep.get("ref")))
            if kind == "Resource":
                resources.add(name)
            owner = doc.get("spec", {}).get("owner", {})
            if owner.get("projectName") != PROJECT:
                errors.append(f"{where}: spec.owner.projectName must be {PROJECT!r}")
            if kind in ("Workload", "ReleaseBinding") and owner.get("componentName") != COMPONENT:
                errors.append(f"{where}: spec.owner.componentName must be {COMPONENT!r}")
            if kind == "ReleaseBinding":
                env = doc["spec"].get("environment")
                if name != f"{COMPONENT}-{env}":
                    errors.append(f"{where}: ReleaseBinding name must be {COMPONENT}-{env}")
            if kind == "ResourceReleaseBinding":
                res, env = owner.get("resourceName"), doc["spec"].get("environment")
                resource_bindings.append((where, res, env))
                if name != f"{res}-{env}":
                    errors.append(f"{where}: ResourceReleaseBinding name must be {res}-{env}")
                # OpenChoreo 1.3.3 cuts ResourceReleases named <resource>-<hash> and never sets the pin itself.
                if not str(doc["spec"].get("resourceRelease", "")).startswith(f"{res}-"):
                    errors.append(f"{where}: spec.resourceRelease must pin a release {res}-<hash>")

    # OpenChoreo rejects a binding whose secretKeyRef names a missing SecretReference or key; fail here first.
    for where, ref_name, key in secret_key_refs:
        if key not in secret_refs.get(ref_name, set()):
            errors.append(f"{where}: secretKeyRef {ref_name}/{key} has no SecretReference with that secretKey")

    # A Workload dependency on an undeclared Resource, or one without a development binding, blocks the autoDeployed
    # development release (ResourceDependenciesPending); a binding of an undeclared Resource never becomes Ready.
    bound_dev = {res for _, res, env in resource_bindings if env == "development"}
    for where, ref in resource_refs:
        if ref not in resources:
            errors.append(f"{where}: dependencies.resources ref {ref!r} has no Resource")
        elif ref not in bound_dev:
            errors.append(f"{where}: Resource {ref!r} has no ResourceReleaseBinding for development")
    for where, res, _ in resource_bindings:
        if res not in resources:
            errors.append(f"{where}: spec.owner.resourceName {res!r} has no Resource")

    kinds = {k for k, _ in seen}
    for required in ("Component", "Workload", "ReleaseBinding"):
        if required not in kinds:
            errors.append(f"missing a {required}")

    for e in errors:
        print(f"ERROR {e}")
    print(f"validated {len(seen)} objects, {len(errors)} error(s)")
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main())
