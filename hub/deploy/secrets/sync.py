#!/usr/bin/env python3
"""Bootstrap hub platform Secrets from 1Password, without files or value output."""
import argparse
import base64
import json
import subprocess
import sys

VAULT = "LOLA"
# Namespace, Secret, item, fields, Kubernetes Secret type.
SECRETS = [
    ("lola-hub", "lola-hub-env", "lola-hub-production", ["LOLA_HUB_SECRET_KEY"], "Opaque"),
    ("lola-hub", "registry-sushidev-registry-01", "lola-hub-registry-production", [".dockerconfigjson"], "kubernetes.io/dockerconfigjson"),
    ("argocd", "registry-sushidev-registry-01", "lola-hub-registry-production", [".dockerconfigjson"], "kubernetes.io/dockerconfigjson"),
    ("argocd", "argocd-valkey-auth", "lola-argocd-valkey-production", ["redis-password"], "Opaque"),
    ("crowdsec", "crowdsec-machine-auth", "lola-crowdsec-production", ["csLapiSecret", "registrationToken"], "Opaque"),
    ("crowdsec", "crowdsec-bouncer", "lola-crowdsec-production", ["BOUNCER_KEY_traefik"], "Opaque"),
    ("traefik", "crowdsec-bouncer", "lola-crowdsec-production", ["BOUNCER_KEY_traefik"], "Opaque"),
]


def command(argv, payload=None):
    result = subprocess.run(argv, input=payload, text=True, capture_output=True, timeout=90)
    if result.returncode:
        # CLI error text can contain values: report only the executable and status.
        raise RuntimeError(f"{argv[0]} failed (exit {result.returncode}); no sensitive output displayed")
    return result.stdout


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--context", help="Explicit destination kube context (required unless --check)")
    parser.add_argument("--check", action="store_true", help="Validate vault fields only; do not contact Kubernetes")
    args = parser.parse_args()
    if not args.check and not args.context:
        parser.error("--context is required when applying Secrets")

    items = {}
    for title in dict.fromkeys(row[2] for row in SECRETS):
        item = json.loads(command(["op", "item", "get", title, "--vault", VAULT, "--format", "json"]))
        fields = {}
        for field in item.get("fields", []):
            label = field.get("label")
            if label in fields:
                raise RuntimeError(f"Ambiguous field in {title}; refused to choose")
            if label:
                fields[label] = field.get("value", "")
        items[title] = fields

    manifests = []
    for namespace, name, title, keys, kind in SECRETS:
        data = {}
        for key in keys:
            value = items[title].get(key)
            if not isinstance(value, str) or not value:
                raise RuntimeError(f"Missing required field {title}/{key}")
            if key != ".dockerconfigjson" and value != value.strip():
                raise RuntimeError(f"Unexpected surrounding whitespace in {title}/{key}; refused to change it")
            if key == ".dockerconfigjson":
                config = json.loads(value)
                if not isinstance(config, dict) or not config.get("auths"):
                    raise RuntimeError("Registry item has no Docker auths")
            data[key] = base64.b64encode(value.encode()).decode()
        manifests.append({
            "apiVersion": "v1", "kind": "Secret",
            "metadata": {"name": name, "namespace": namespace,
                         "annotations": {"lola.sushidev.kitchen/secret-source": f"1password/{VAULT}/{title}"}},
            "type": kind, "data": data,
        })
    if args.check:
        print(f"Validated {len(items)} items in {VAULT}; no values printed, no cluster changes")
        return
    payload = json.dumps({"apiVersion": "v1", "kind": "List", "items": manifests})
    command(["kubectl", "--context", args.context, "apply", "--server-side",
             "--field-manager=lola-secrets", "-f", "-"], payload)
    for manifest in manifests:
        metadata = manifest["metadata"]
        print(f"Synced {metadata['namespace']}/{metadata['name']} from {VAULT}")
    print("Environment-based consumers need a controlled restart after credential rotation.")


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, RuntimeError, subprocess.TimeoutExpired):
        print("Secret sync failed; no values or subprocess output displayed. Check CLI authentication, required vault fields and destination access.", file=sys.stderr)
        sys.exit(1)
