# driftgcp — Terraform Drift Detector for GCP

## Files in this branch
- README.md (this file)
- prompt.md
- ai-chat-export.json ← required
- demo/ (screenshots) — not yet added, pending a manual screen capture
- full project tree at repo root (cmd/, internal/, configs/, testdata/)

**Name + Role:** Rubika Venkatesh — DevOps Engineer

**Problem I solved:** Terraform state silently drifts from live GCP infrastructure — someone changes a resource in the console, a script runs outside Terraform, an apply partially fails — and there's no fast way to know it happened until something breaks.

**What I built:** An AI-assisted drift detector (`driftgcp`) that parses a Terraform state file (local disk or a live `gs://` GCS backend), fetches the live state of the same resources directly from GCP APIs (Compute, Storage, VPC, IAM, Pub/Sub, BigQuery), diffs expected vs. actual across 10 resource types, and — when asked (`--explain`) — calls Claude (with real tool-calling, not just a single prompt) to turn the raw diff into a plain-English summary with prioritized remediation steps. Ships as a CLI, a scheduler (`watch`, with Slack webhook alerting on drift), and a web dashboard with real Postgres-backed multi-user auth (teams, roles, an admin panel) instead of a single demo login. Deployed and running live on a QA GKE cluster, reading a real Terraform state straight from GCS via Workload Identity — no key file in the cluster. Validated end-to-end against a real GCP project, where it caught real drift (an untracked label and a versioning setting) on a live storage bucket.

**Tool used:** Claude Code

**Time without AI:** ~1-2 hrs per drift investigation, manually diffing `terraform plan` output against the GCP console, resource by resource.

**Time with AI today:** Built in a single session; a real scan against a live GCP project runs and reports drift in seconds.

**Will I use this next week?** YES — it already caught real drift on a live bucket during setup, and the dashboard makes rerunning it a one-click check.

**Where it lives:** this repository

---

## What it does

```
Terraform State (terraform.tfstate)      Cloud Provider (GCP)
        │                                        │
   State Reader                            GCP Fetcher
        │                                        │
  Resource Extractor                     Resource Extractor
        │                                        │
 Expected Resource Model                 Actual Resource Model
        └───────────────┬────────────────────────┘
                   Drift Engine
                  (compare expected vs actual)
                         │
                  Report Generator
                    ┌────┴────┐
              Console Output  JSON Report
```

Supports 10 resource types (Compute instances, disks, addresses, networks,
subnetworks, firewalls; Storage buckets; IAM service accounts; Pub/Sub
topics; BigQuery datasets), each with a curated set of compared attributes
to avoid false-positive drift from computed/read-only GCP fields.

## Run it

No GCP account needed — bundled fixture data with deliberate drift baked in:

```sh
go build -o driftgcp ./cmd/driftgcp
./driftgcp scan --config configs/driftgcp.yaml
```

With a real GCP project:

```sh
gcloud auth application-default login
./driftgcp scan --state terraform.tfstate --provider gcp --project my-gcp-project
```

AI-generated plain-English summary + remediation steps:

```sh
export ANTHROPIC_API_KEY=sk-ant-...
./driftgcp scan --config configs/driftgcp.yaml --explain
```

Web dashboard:

```sh
./driftgcp serve --addr :8080
```

The dashboard sits behind a login page, with two auth backends:

**No `DATABASE_URL` (default, zero setup)** — the original single demo
account, session kept in memory (lost on restart). Sign in with
`DRIFTGCP_USER` / `DRIFTGCP_PASS` if set, otherwise `demo` / `driftgcp-demo`:

```sh
export DRIFTGCP_USER=demo
export DRIFTGCP_PASS=driftgcp-demo
./driftgcp serve --addr :8080
```

**With `DATABASE_URL` set — real multi-user Postgres auth.** Users, teams
(one team per user), bcrypt-hashed passwords, and sessions all live in
Postgres (`internal/store`, migrations in
`internal/store/migrations/`), so sessions survive a restart. Spin up a
local Postgres with the bundled `docker-compose.yml`:

```sh
docker compose up -d
export DATABASE_URL=postgres://driftgcp:driftgcp@localhost:5433/driftgcp?sslmode=disable
export DRIFTGCP_ADMIN_EMAIL=you@example.com
export DRIFTGCP_ADMIN_PASSWORD=choose-a-password
./driftgcp serve --addr :8080
```

`DRIFTGCP_ADMIN_EMAIL`/`DRIFTGCP_ADMIN_PASSWORD` are read **only once**, to
create the first team ("default", or set `DRIFTGCP_BOOTSTRAP_TEAM`) and its
first admin user when the database is empty. After that, sign in as that
admin and manage everything from the dashboard's **Team admin** panel:
add teammates (member/admin role) and assign which Terraform state files
your team can see and scan. A non-admin state file the user's team doesn't
own is rejected server-side (`403`), not just hidden from the dropdown.

Known gaps in this design, worth knowing before relying on it:
- **One team per user** — a user can't belong to multiple teams. Creating
  a *second* team currently requires direct DB access (`store.CreateTeam`
  / a fresh `EnsureBootstrapAdmin` call), not a dashboard flow — the admin
  panel only manages membership within your own team.
- **GCP project list is not team-scoped** — it's still the live Cloud
  Resource Manager lookup, unrestricted by team. Only the Terraform
  state-file list is team-scoped.
- **DRIFTGCP_USER/DRIFTGCP_PASS mode and DATABASE_URL mode are mutually
  exclusive**: when `DATABASE_URL` is set, the legacy env-var login stops
  working entirely (by design, per the original requirement).

Watch mode — re-scan on an interval, log only the drift that's new since
the last run, and notify a webhook (Slack-compatible) when a run finds
critical drift:

```sh
export DRIFTGCP_WEBHOOK_URL=https://hooks.slack.com/services/...
./driftgcp watch --config configs/driftgcp.yaml --interval 15m
```

The dashboard has the same watch mode as a single on/off toggle — no
interval or webhook fields in the UI. Both are configured server-side only,
via `DRIFTGCP_WATCH_INTERVAL_MINUTES` and `DRIFTGCP_WEBHOOK_URL` (same var
the CLI uses), so the webhook URL is never sent to or displayed in the
browser — set them before `driftgcp serve`:

```sh
export DRIFTGCP_WATCH_INTERVAL_MINUTES=15
export DRIFTGCP_WEBHOOK_URL=https://hooks.slack.com/services/...
./driftgcp serve --addr :8080
```

The "Run scan" button posts to the same webhook too, on *every* click —
clean or drift, with or without `--explain` — so Slack doubles as an audit
trail of manual scans, not just watch-mode alerts. When explain is
requested, Claude's summary is included in the Slack message body.

## QA GKE deployment

Deployed to a GKE cluster (namespace `driftgcp`), Postgres auth mode,
Workload Identity for GCP access — see `deploy/k8s/*.yaml` and the
`Dockerfile`. Real cluster/project/bucket/hostname values are intentionally
not included here; substitute your own.

**One-time setup already done** (not re-run on every deploy):
```sh
gcloud container clusters get-credentials <cluster-name> --zone <zone> --project <gcp-project-id> --dns-endpoint
kubectl apply -f - <<'EOF'
apiVersion: v1
kind: Namespace
metadata: { name: driftgcp, labels: { team: devops } }
EOF
gcloud artifacts repositories create driftgcp --repository-format=docker --location=<region> --project=<gcp-project-id>
gcloud iam service-accounts create driftgcp-qa --project=<gcp-project-id>
gcloud projects add-iam-policy-binding <gcp-project-id> \
  --member="serviceAccount:driftgcp-qa@<gcp-project-id>.iam.gserviceaccount.com" --role="roles/editor" --condition=None
gcloud iam service-accounts add-iam-policy-binding driftgcp-qa@<gcp-project-id>.iam.gserviceaccount.com \
  --project=<gcp-project-id> --role="roles/iam.workloadIdentityUser" \
  --member="serviceAccount:<gcp-project-id>.svc.id.goog[driftgcp/driftgcp]" --condition=None
kubectl apply -n driftgcp -f - <<'EOF'
apiVersion: v1
kind: ServiceAccount
metadata:
  name: driftgcp
  namespace: driftgcp
  annotations: { iam.gke.io/gcp-service-account: driftgcp-qa@<gcp-project-id>.iam.gserviceaccount.com }
EOF
```
`driftgcp-qa` was granted `roles/editor` on the project, matching the scope
of an existing local key exactly, per an explicit choice; this is broader
than a read-only drift scanner strictly needs.

**Secrets** (created imperatively, never committed):
`driftgcp-postgres-secret` (POSTGRES_USER/PASSWORD/DB) and
`driftgcp-app-secret` (ANTHROPIC_API_KEY, DRIFTGCP_WEBHOOK_URL,
DRIFTGCP_ADMIN_EMAIL, DRIFTGCP_ADMIN_PASSWORD, DATABASE_URL pointing at the
in-cluster `driftgcp-postgres` Service — a plain Postgres pod + PVC, not
Cloud SQL, by design).

**Reading state from a GCS Terraform backend:** `--state`/`state_path` now
also accepts `gs://bucket/object` (`internal/state/reader.go`), so a real
`backend "gcs" {}` Terraform config's state can be scanned live from GKE via
Workload Identity, with no rebuild when the state changes. Verified
end-to-end against a dedicated, versioned state bucket (not any shared org
state bucket) — same drift result as scanning the equivalent local file.

**Redeploying after a code change:**
```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/driftgcp-linux-amd64 ./cmd/driftgcp
docker build --platform linux/amd64 -t <region>-docker.pkg.dev/<gcp-project-id>/driftgcp/driftgcp:vN .
docker push <region>-docker.pkg.dev/<gcp-project-id>/driftgcp/driftgcp:vN
# bump the image tag in deploy/k8s/app.yaml, then:
kubectl apply -f deploy/k8s/app.yaml
kubectl rollout status deployment/driftgcp -n driftgcp
```
Cross-compiling the binary on the host first (rather than `go build` inside
an emulated `linux/amd64` build stage) avoids a QEMU crash seen when
building for amd64 on an Apple Silicon host.

**Exposing it:** the Kubernetes Service is ClusterIP-only by default. An
Ingress (`deploy/k8s/app.yaml`) is provided for nginx + cert-manager, but a
DNS record pointing at your ingress controller's external IP is a separate,
deliberate step — nothing here creates one automatically. Until you add
one, reach it with:
```sh
kubectl port-forward -n driftgcp svc/driftgcp 18080:8080
```
(verified working end-to-end this way, including login and a scan).

## Tools / Technologies

Go, Cobra CLI, Terraform state (raw JSON, no `terraform show` shell-out),
Google Cloud APIs (Compute, Storage, IAM, Pub/Sub, BigQuery), Anthropic
Claude API with tool calling (`--explain` — Claude calls a local tool to
look up the exact terraform import ID format per resource type instead of
guessing it), embedded static web dashboard with session-cookie login,
webhook alerting, a scheduler (`watch`), a Jenkins pipeline (`Jenkinsfile`:
build/vet/test), and — with `DATABASE_URL` set — Postgres-backed multi-user
auth (`database/sql` + `pgx/v5/stdlib`, `golang.org/x/crypto/bcrypt`, plain
SQL migrations in `internal/store/migrations/`, no ORM), plus Docker/GKE/
Workload Identity for deployment.
