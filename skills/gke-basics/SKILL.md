---
name: gke-basics
license: Apache-2.0
metadata:
  author: Google Cloud
  version: "1.0.0"
description: "Plan, create, and configure production-ready Google Kubernetes Engine (GKE) clusters using the golden path Autopilot configuration. Covers Day-0 checklist, networking, security, scaling, and cost optimization. Use when creating or configuring GKE. Do NOT use for non-GKE Kubernetes (EKS/AKS), Cloud Run without Kubernetes (use cloud-run-deploy), or local Docker (use docker)."
---

# Google Kubernetes Engine (GKE) Basics

GKE is a managed Kubernetes platform on Google Cloud for deploying, scaling, and operating containerized applications. This skill defaults to the **golden path Autopilot configuration** — see [gke-golden-path.md](./references/gke-golden-path.md) for defaults, rules, and guardrails.

**When NOT to use this skill:** Non-GKE Kubernetes (EKS, AKS, vanilla k8s), Cloud Run without Kubernetes, or GCE-only deployments without containers.

## Harness overrides

This skill is a fork of Google's `gke-basics` kept under its upstream name (`skills/VENDORED.md`, second table); only this file carries local text, the references and assets are verbatim. Where they say otherwise, these rules win:

- **gcloud and kubectl are the tools.** The references' "MCP tools > gcloud > kubectl" preference (`cli-reference.md`, `gke-golden-path.md`, `mcp-usage.md`) does not apply: no GKE MCP server is configured here, and the harness runs no MCP for infrastructure. Read `mcp-usage.md` as documentation of a tool that is absent.
- **The project is asked for once.** `gke-golden-path.md` says never to ask for a project ID and `gke-cluster-creation.md` says ALWAYS ask; the global context's GCP rule settles it: the operator gives the project ID, region and the other defaults once and they are recorded in `docs/deploy/gcp.md`, which the next session reads instead of asking again. Never `gcloud config get-value project` as a source.
- **Two paths in the references are known-dangling.** `gke-security.md` applies `skills/gke/assets/default-deny-netpol.yaml`: the file is `skills/gke-basics/assets/default-deny-netpol.yaml` (installed at `~/.gemini/config/skills/gke-basics/assets/`). `assets/golden-path-autopilot.yaml` cites `resources/recommended-ap.yaml`, an upstream export that was never vendored; the YAML in the asset is the whole of what exists.
- **Samples are illustrative, not copy-paste.** `iac-usage.md`'s `deletion_protection = false` is for a lab cluster only; production keeps the default `true`. `client-library-usage.md`'s Go sample discards every error and passes `context.TODO` uncalled; real code checks each `err` and passes a `context.Context` (the `golang` skill). Every `us-central1` in the references is a sample value, never a default: `iac-usage.md:23` (`location`), `gke-compute-classes.md:96-97` (zones), `gke-golden-path.md:19`, and `gke-reliability.md:11,32`. The region is the operator's, from `docs/deploy/gcp.md`, and every `gcloud` command carries `--project` and `--region`, as the Quick Start does. `gke-app-onboarding.md`'s Dockerfile pins an old Go minor in its `FROM golang:` line: use `golang:<major.minor of the go.mod go line>` (the `docker` skill).

## Quick Start

```bash
# PROJECT_ID and REGION come from the project's docs/deploy/gcp.md, never assumed
gcloud services enable container.googleapis.com --project="$PROJECT_ID"
gcloud container clusters create-auto my-cluster --project="$PROJECT_ID" --region="$REGION"
gcloud container clusters get-credentials my-cluster --project="$PROJECT_ID" --region="$REGION"
kubectl create deployment hello-server \
  --image=us-docker.pkg.dev/google-samples/containers/gke/hello-app:1.0
```

## Reference Directory

Load the relevant reference based on trigger keywords. Prefer the most specific match; if ambiguous, ask the user to clarify.

| Scenario | Trigger Keywords | Reference |
|----------|-----------------|-----------|
| Core Concepts | Autopilot vs Standard, architecture, pricing, what is GKE | [core-concepts.md](./references/core-concepts.md) |
| Golden Path & Defaults | golden path, Day-0 checklist, production defaults, cluster defaults | [gke-golden-path.md](./references/gke-golden-path.md) |
| Cluster Creation | create cluster, new cluster, provision GKE | [gke-cluster-creation.md](./references/gke-cluster-creation.md) |
| Networking | private cluster, VPC, subnet, Gateway API, DNS, ingress, egress, datapath | [gke-networking.md](./references/gke-networking.md) |
| Security & IAM | Workload Identity, Secret Manager, RBAC, Binary Auth, hardening, audit, gVisor, IAM roles | [gke-security.md](./references/gke-security.md) |
| Scaling | HPA, VPA, autoscaler, autoscaling, NAP, scale pods, scale nodes | [gke-scaling.md](./references/gke-scaling.md) |
| Compute Classes | ComputeClass, machine family, Spot fallback, GPU node pool, node selection | [gke-compute-classes.md](./references/gke-compute-classes.md) |
| Cost | cost, savings, Spot VMs, rightsizing, CUD, optimize spend, budget | [gke-cost.md](./references/gke-cost.md) |
| AI/ML Inference | inference, model serving, LLM, GPU, TPU, GIQ, vLLM | [gke-inference.md](./references/gke-inference.md) |
| Upgrades | upgrade, maintenance window, release channel, patching, version | [gke-upgrades.md](./references/gke-upgrades.md) |
| Observability | monitoring, logging, Prometheus, Grafana, metrics, alerts, dashboards | [gke-observability.md](./references/gke-observability.md) |
| Multi-tenancy | multi-tenant, namespace isolation, team access, enterprise, RBAC planning | [gke-multitenancy.md](./references/gke-multitenancy.md) |
| Batch & HPC | batch, HPC, job queue, high performance, MPI, parallel | [gke-batch-hpc.md](./references/gke-batch-hpc.md) |
| App Onboarding | containerize, deploy app, Dockerfile, onboard, migrate to GKE | [gke-app-onboarding.md](./references/gke-app-onboarding.md) |
| Backup & DR | backup, restore, disaster recovery, CMEK | [gke-backup-dr.md](./references/gke-backup-dr.md) |
| Storage | storage, PVC, persistent volume, StorageClass, Filestore, GCS FUSE | [gke-storage.md](./references/gke-storage.md) |
| Reliability | PDB, health probe, liveness, readiness, topology spread, graceful shutdown | [gke-reliability.md](./references/gke-reliability.md) |
| Client Libraries | client library, client-go, kubernetes python, kubernetes java, kubernetes SDK | [client-library-usage.md](./references/client-library-usage.md) |
| Infrastructure as Code | Terraform, IaC, HCL, infrastructure as code | [iac-usage.md](./references/iac-usage.md) |
| MCP Server | MCP tools, MCP server, MCP setup | [mcp-usage.md](./references/mcp-usage.md) |
| CLI / Tools | gcloud, kubectl, commands, how to | [cli-reference.md](./references/cli-reference.md) |
| Production Audit | production readiness, compliance, golden path check | [gke-cluster-creation.md](./references/gke-cluster-creation.md) |

*If you need product information not found in these references, consult the [official GKE documentation](https://cloud.google.com/kubernetes-engine/docs).*
