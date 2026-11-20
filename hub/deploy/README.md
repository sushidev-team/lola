# Hub on DigitalOcean with Argo CD

This replaces GitHub's direct deployment to `sushidev-cluster-02`. Run commands from the
repository root. The manifests retain `lola.sushidev.kitchen` and the existing registry;
change those names in Git before bootstrap if needed. The production cluster is `lola-prod-01`
(context `do-fra1-lola-prod-01`, Kubernetes 1.37.2, three `s-2vcpu-4gb` nodes in `fra1`).

## Deployment flow

```text
main (hub/ or hubproto/ changes)
  → GitHub: docker build (including make check) + push :<sha> and :latest
  → Image Updater: poll :latest every 30 seconds, resolve immutable digest
  → update lola-hub Application's Kustomize image override
  → Argo CD auto-sync → Recreate rollout → /healthz readiness

Internet → DO network LB → Traefik → CrowdSec bouncer + AppSec WAF → hub
```

Image Updater uses `argocd` write-back: no Git write credentials, deployment webhook,
GitHub kubeconfig or GitHub hub encryption key. The root Application preserves the image
override with `ignoreDifferences` **and** `RespectIgnoreDifferences=true`. Manifests track
`main`; charts and the bouncer plugin have explicit versions. Valkey is an authenticated,
internal **Argo cache**, without persistence. The hub does not use it; the bouncer uses
its own memory cache. Traefik replicas require different worker nodes.

Target: usually 1–2 minutes **after publishing finishes**, plus slow image pulls/volume
attaches. This is not a latency guarantee. `imagePullPolicy: Always` alone would not trigger
updates; changing the digest does. Closely spaced builds can coalesce to the latest image;
this does not guarantee running every intermediate commit. Published images also have SHA
tags and OCI revision labels. GitHub serializes main builds to prevent an older concurrent
build from overwriting `latest` after a newer one.

The hub must stay **one replica, Recreate**: JSON storage and relay rendezvous are
single-process. Each rollout briefly interrupts the API and disconnects relay WebSockets;
daemons reconnect and phones re-race. Multiple worker nodes do not make the hub HA.

## DigitalOcean resources

Start here for a small production installation. These are estimates, not load-test results.

| Resource | Recommendation |
|---|---|
| Region / network | `fra1`, one VPC; volumes/registry in the same region |
| DOKS | Current supported Kubernetes compatible with pinned charts, at least 1.30 |
| Worker pool | 3 × Basic Regular `s-2vcpu-4gb`, amd64: total 6 vCPU / 12 GiB |
| Autoscaling | Optional min 3 / max 5, with maintenance headroom |
| Control plane | Standard; optionally HA for production Kubernetes API availability |
| Public entry | One `REGIONAL_NETWORK` LoadBalancer, size 1, TCP 80/443 |
| Hub data | 10 GiB RWO volume, `Retain`, `/data/hub/hub.json` and admin token |
| CrowdSec data | 10 GiB RWO volume, SQLite, one LAPI replica |
| Valkey | In-cluster, single instance, 128 MiB request / 256 MiB limit |
| Registry | Reuse `sushidev-registry-01`, retain recent SHA tags for rollback |
| Backups | Daily volume snapshots + encrypted off-cluster copy; key backed up separately |
| DNS | Domain A record → Traefik LB; TTL 60 during migration |

Configured application requests are about 1.6 vCPU / 2.5 GiB with three CrowdSec log agents,
before Kubernetes/CSI/Cilium overhead. Three nodes leave room for rescheduling, WAF bursts
and upgrades. Two nodes are a budget option; one is for testing. The hub starts at 100m CPU /
128 MiB, limited to 1 CPU / 512 MiB. Monitor relay concurrency, CPU/OOMs, disk and bandwidth.
Alert on unavailable hub, unhealthy Argo apps, updater errors, WAF failure and disk >80%.
Set bounded ingress log retention: CrowdSec needs client IPs.

Pricing checked 2026-11-20: workers **$72/month**, network LB **$15/month**, 20 GiB volumes
**$2/month**: **about $89/month**, excluding registry, snapshots, backup storage, tax and
bandwidth overages. Optional HA control plane adds $40/month. Network LB size 1 adds
50 Mbps ingress capacity; raise its size if relay traffic requires it.
Sources: [workers](https://www.digitalocean.com/pricing/droplets),
[LB](https://docs.digitalocean.com/products/networking/load-balancers/details/pricing/),
[volumes](https://docs.digitalocean.com/products/volumes/details/pricing/),
[control plane](https://www.digitalocean.com/pricing/kubernetes).

Use DOKS-managed node firewall rules, restrict Kubernetes API access to operator networks
where practical, and expose applications through the LB. NetworkPolicies require an enforcing
CNI (DOKS/Cilium). Do not expose the admin console or node ports directly.

## Bootstrap a new cluster

Merge these files into `main` before Argo reads them. Provision the cluster in DigitalOcean;
this repository does not create a paid cluster. Use a dedicated context and verify it.
This installs Argo into a **new, dedicated** namespace. For an existing shared Argo/Traefik
installation, merge values into its owning GitOps repository instead of installing duplicates
or changing its cache in place.

The dedicated `lola-block-storage-retain` class is Git-managed. DOKS already provides
`do-block-storage-retain` with immutable `Immediate` binding; this setup leaves that managed
class untouched and uses `WaitForFirstConsumer` on its own class.

### 1. Namespaces, storage, secrets

```sh
doctl kubernetes cluster kubeconfig save --set-current-context=false lola-prod-01
kubectl --context do-fra1-lola-prod-01 get nodes
kubectl --context do-fra1-lola-prod-01 create namespace argocd
kubectl --context do-fra1-lola-prod-01 apply -k hub/deploy/argocd/cluster
kubectl --context do-fra1-lola-prod-01 apply -f hub/deploy/k8s/overlays/production/namespace.yaml
```

The source of truth is the **`LOLA` 1Password vault**. Its items map to cluster Secrets:

| 1Password item | Fields | Kubernetes consumers |
|---|---|---|
| `lola-hub-production` | `LOLA_HUB_SECRET_KEY` | `lola-hub/lola-hub-env` |
| `lola-hub-registry-production` | `.dockerconfigjson` | Hub pulls and Argo Image Updater |
| `lola-argocd-valkey-production` | `redis-password` | Argo + Valkey |
| `lola-crowdsec-production` | `csLapiSecret`, `registrationToken`, `BOUNCER_KEY_traefik` | CrowdSec + Traefik |

The existing encryption key and registry credentials were imported unchanged from the running
hub in **`do-fra1-sushidev-cluster-02`**, namespace `lola-hub`. On 2026-11-20 the supplied
`do-fra1-sushidev-stage-01` context contained no Lola namespace, Deployment, Argo Application
or OnePasswordItem; it was not the source of the running hub's secrets. The stage cluster's
shared Argo/1Password installation is left untouched. New Valkey/CrowdSec credentials are
prepared in the vault for the new cluster. Existing TLS certificates are managed by cert-manager
and are reissued, not copied into the vault. The console token travels with the data volume.

After authenticating `op`, materialize the seven required Kubernetes Secrets, with the explicit
**new** context. The script reads values and transmits Secret JSON only through process pipes;
it never writes secret files or prints values. It does not read the GHA service-account token.

```sh
# Use desktop integration if authenticating interactively:
OP_BIOMETRIC_UNLOCK_ENABLED=true op signin
OP_BIOMETRIC_UNLOCK_ENABLED=true python3 hub/deploy/secrets/sync.py --check
OP_BIOMETRIC_UNLOCK_ENABLED=true python3 hub/deploy/secrets/sync.py --context do-fra1-lola-prod-01
```

This is an explicit bootstrap/rotation step, not a live vault watcher. A configured `op` service
account can run the same script; scope it to these items and do not commit its token. Argo never
receives a personal 1Password login. The vault's `Service Account Auth Token: GHA LOLA` item is
preserved; image-only CI does not need to read the vault.

Check imported registry credential expiry/scope before reuse; use a renewable read-only pull
credential. Store its Docker auth JSON in the registry item and re-run the sync on rotation.
Image Updater refreshes credentials hourly. Hub env changes require a controlled
`kubectl --context do-fra1-lola-prod-01 -n lola-hub rollout restart deploy/lola-hub`; coordinate CrowdSec/Valkey rotations with
all consumers too. **Never rotate the hub encryption key without re-encrypting existing data.**

### 2. Install Argo and Valkey once

```sh
helm repo add argo https://argoproj.github.io/argo-helm
helm repo add valkey https://valkey.io/valkey-helm/
helm repo update
helm upgrade --install argocd-valkey valkey/valkey --version 1.0.0 \
  --kube-context do-fra1-lola-prod-01 -n argocd -f hub/deploy/argocd/values/valkey.yaml --wait --timeout 5m
helm upgrade --install argocd argo/argo-cd --version 10.18.2 \
  --kube-context do-fra1-lola-prod-01 -n argocd -f hub/deploy/argocd/values/argocd.yaml --wait --timeout 10m
```

Argo then adopts those same resources; subsequent changes go through Git. For a private
repository, configure an Argo read-only repository credential before continuing.
**Complete the migration below before applying the root Application**, or skip it for an
empty hub.

### Migration from the old cluster

Use a maintenance window. Lower DNS TTL first, wait for old deploy jobs to finish, and disable
any old reconciler. Stop the old hub for the final copy; never run two public hubs against
independent copies of the store. Retain the old PVC and a volume snapshot until verified.

```sh
hub_old_context=do-fra1-sushidev-cluster-02
hub_new_context=do-fra1-lola-prod-01
umask 077
hub_migration_tmp=$(mktemp -d)
# Protect this directory until the archive has been secured and restoration verified.
kubectl --context "$hub_old_context" -n lola-hub scale deploy/lola-hub --replicas=0
kubectl --context "$hub_old_context" -n lola-hub wait --for=delete pod -l app=lola-hub --timeout=180s
kubectl --context "$hub_old_context" apply -f hub/deploy/maintenance/data-pod.yaml
kubectl --context "$hub_old_context" -n lola-hub wait \
  --for=condition=Ready pod/lola-hub-data-maintenance --timeout=300s
kubectl --context "$hub_old_context" -n lola-hub exec lola-hub-data-maintenance \
  -- tar -C /data -czf - hub > "$hub_migration_tmp/hub-data.tgz"
tar -tzf "$hub_migration_tmp/hub-data.tgz"
kubectl --context "$hub_old_context" -n lola-hub delete pod lola-hub-data-maintenance --wait=true

kubectl --context "$hub_new_context" -n lola-hub apply -f hub/deploy/k8s/base/pvc.yaml
kubectl --context "$hub_new_context" apply -f hub/deploy/maintenance/data-pod.yaml
kubectl --context "$hub_new_context" -n lola-hub wait \
  --for=condition=Ready pod/lola-hub-data-maintenance --timeout=300s
kubectl --context "$hub_new_context" -n lola-hub exec -i lola-hub-data-maintenance \
  -- tar -C /data -xzpf - < "$hub_migration_tmp/hub-data.tgz"
kubectl --context "$hub_new_context" -n lola-hub exec lola-hub-data-maintenance \
  -- chmod 0700 /data/hub
kubectl --context "$hub_new_context" -n lola-hub exec lola-hub-data-maintenance \
  -- find /data/hub -type f -exec chmod 0600 '{}' +
kubectl --context "$hub_new_context" -n lola-hub exec lola-hub-data-maintenance -- ls -ln /data/hub
kubectl --context "$hub_new_context" -n lola-hub delete pod lola-hub-data-maintenance --wait=true
```

The archive includes snapshots, backups and `admin.token` with UID 65532. The maintenance
pod uses `OnRootMismatch` to avoid changing existing file modes when mounting the volume.
Restore preserves archive modes and explicitly re-establishes 0700 directories / 0600 files;
older archives may have inherited wider group permissions from an earlier mount.
Confirm `hub.json` and `admin.token` exist, and restore the same encryption key before startup.
The hub is distroless: `kubectl cp` would fail because that image has no `tar`.

### 3. Start GitOps, switch DNS

```sh
kubectl --context do-fra1-lola-prod-01 apply -f hub/deploy/argocd/bootstrap.yaml
kubectl --context do-fra1-lola-prod-01 -n argocd get applications -w
```

Waves install cluster resources → Valkey → Argo → cert-manager / Image Updater → CrowdSec /
Traefik → issuer / updater config → hub. Application health customization makes the root
wait for child apps. CRDs use server-side apply.

```sh
kubectl --context do-fra1-lola-prod-01 -n traefik get service traefik
```

Point the domain's A record at that LB IP; remove stale AAAA records unless IPv6 is configured.
Wait for `kubectl --context do-fra1-lola-prod-01 -n lola-hub get certificate lola-hub-tls` to become Ready. HTTP-01 needs
port 80. The WAF is on the hub HTTPS route, leaving the separate ACME solver route accessible.
HTTP-01 completes only after DNS points to the new cluster; use DNS-01 if TLS must be ready
before cutover.

GitHub now needs only its registry push credential (`DO_ACCESS_TOKEN`, scoped for registry).
The Hub publishing workflow has no deploy job, kubeconfig, kubectl or environment-secret writes.
`DO_REGISTRY` and GitHub's `LOLA_HUB_SECRET_KEY` are no longer read by it; remove old deployment
credentials/environment gates after checking other workflows. Independent `hub-check.yml`
preserves PR checks. Docker also runs `make check`, so failing tests cannot publish an image.

## Verify and operate

```sh
kubectl --context do-fra1-lola-prod-01 -n argocd get applications
kubectl --context do-fra1-lola-prod-01 -n argocd get imageupdater lola-hub -o yaml
kubectl --context do-fra1-lola-prod-01 -n argocd logs deploy/argocd-image-updater-controller --tail=50
kubectl --context do-fra1-lola-prod-01 -n lola-hub rollout status deploy/lola-hub --timeout=300s
kubectl --context do-fra1-lola-prod-01 -n lola-hub get pods -o jsonpath='{.items[*].status.containerStatuses[*].imageID}'
curl --fail https://lola.sushidev.kitchen/healthz
kubectl --context do-fra1-lola-prod-01 -n crowdsec exec deploy/crowdsec-lapi -- cscli bouncers list
kubectl --context do-fra1-lola-prod-01 -n crowdsec exec deploy/crowdsec-appsec -- cscli metrics
kubectl --context do-fra1-lola-prod-01 -n crowdsec logs daemonset/crowdsec-agent --tail=50
```

Compare the running digest with the published SHA image. Check bouncer validity, decision
pulls, Traefik log acquisition and AppSec traffic. On your controlled test route, check that a
known WAF test request is blocked and benign traffic succeeds. Check source IPs from two
networks and ensure a forged `X-Forwarded-For` does not change them. Test a real phone relay
and long poll across a rollout. These require the new cluster: rendered YAML cannot prove
end-to-end enforcement or reconnection.

Traefik uses a source-IP-preserving DO **network LB** and `externalTrafficPolicy: Local`,
with no PROXY protocol or trust of Internet-supplied forwarding headers. NetworkPolicy admits
only Traefik pods to the hub, making its `X-Real-Ip` header trustworthy. Do not switch to an HTTP
LB without updating this trust configuration. See [DO's LB configuration](https://docs.digitalocean.com/products/kubernetes/how-to/configure-load-balancers/).

The WAF is CrowdSec AppSec (virtual patching + generic rules), using the pinned Traefik bouncer
plugin, as in [CrowdSec's integration](https://docs.crowdsec.net/docs/next/appsec/quickstart/traefik/).
The agent detects attacks, the bouncer enforces IP decisions, and AppSec inspects HTTP requests.
AppSec errors/unreachability block requests, intentionally reducing availability during a WAF
outage. The HTTP upgrade is inspected; encrypted WebSocket contents stay opaque. No browser
captcha/challenge is enabled for API clients. Access log headers and query parameters are dropped.

```sh
kubectl --context do-fra1-lola-prod-01 -n argocd port-forward svc/argocd-server 8443:443
# https://localhost:8443; initial password via your normal Argo admin setup.
kubectl --context do-fra1-lola-prod-01 -n lola-hub port-forward deploy/lola-hub 8098:8098
# http://localhost:8098; use the existing admin token.
```

For an empty hub only: `kubectl --context do-fra1-lola-prod-01 -n lola-hub exec deploy/lola-hub -- /lola-hub init --org sushi`.
Secure the one-time token; do not initialize again during migration. Console access remains on
pod loopback, outside Service/Ingress, gated by Kubernetes RBAC for port-forward/exec.

For rollback, commit `commonUpdateSettings.ignoreTags: ["*"]` on the hub image in
`argocd/config/image-updater.yaml` and wait for sync. Then set a known image digest:

```sh
argocd app set lola-hub --kustomize-image \
  registry.digitalocean.com/sushidev-registry-01/lola-hub=registry.digitalocean.com/sushidev-registry-01/lola-hub@sha256:<digest>
```

Argo auto-syncs it and the root preserves the override. `kubectl rollout undo` alone would be
reverted by Argo. Remove `ignoreTags` in Git to resume updates. For schema-changing rollbacks,
check data compatibility and restore a matching backup when needed.

Backups must include the complete hub directory and its separately stored encryption key.
`Retain` is not a backup. Use the maintenance pod with the hub stopped for a consistent archive;
regularly test restoration in isolation. Keep CrowdSec's LAPI volume; Valkey needs no backup.

Local validation: render all six pinned charts, `kustomize build` the production overlay and
both Argo resource directories, validate Application/ImageUpdater objects against matching
CRDs, and run `actionlint` on both Hub workflows.
