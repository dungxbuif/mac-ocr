---
title: Lab CI/CD delivery
sidebar_position: 2
---

# Lab CI/CD delivery

The lab pipeline delivers only the Linux `proxy` component. The macOS native
worker is an external client: it follows the repository Git flow and its local
Mac build/release guide, but it is not built, signed, or deployed by the lab
runner.

## Release contract

Production delivery is tag-driven. Use the shared lab tag format:

```text
prod-<major>.<minor>.<patch>[-<prerelease>]
```

For example, `prod-2.1.0` runs validation, tests, the registry smoke test,
builds the root `Dockerfile` once, pushes the image, updates the existing
`macocr-proxy` Swarm service by immutable digest, and checks `/healthz` and
`/readyz`. The service remains at one replica because WebSocket connections are
owned by one proxy process.

`dev-*` and `staging-*` tags are accepted by the tag grammar for future lab
environments. They do not deploy production. A tag is the only release trigger;
ordinary merge-request and default-branch pipelines validate source changes but
never update the live service.

## Shared lab runner

This project is stored under the private `homelab-apps` group and uses the
shared `homelab-rootless` tag. The Mac runner is the preferred local capacity
for this pool and is one of the two registrations served by the single local
process in `~/dev-env` (`concurrent = 2`), with 48 GiB of host memory for the
parallel Rust/Go image build. The same tag is also used by the VM100
homelab-automation runner where that project is in scope; GitLab does not
provide a strict priority field between eligible runners, so the pool remains
failover-capable rather than binding every repository to a Mac-only tag.

Every job in `.gitlab-ci.yml`, including validation, registry, build, migration,
deploy, and verification, uses `homelab-rootless`. The Mac runner remains
protected and the release tag patterns are protected, so credentials and
deployment jobs remain inside the protected release path. Use `homelab-mac`
only when a future job genuinely requires the Mac host; it is not the default
tag for Homelab CI/CD. The macOS native worker is still an external client: the
lab pipeline does not build or sign that native app.

The Mac Docker-in-Docker service is started with the Mac's lab DNS resolver
(`10.10.0.5`) and a public fallback (`1.1.1.1`). This is required for nested
BuildKit `RUN` steps: the job container can resolve names, but the inner
builder otherwise inherits an unusable nested resolver and `apk` cannot reach
Alpine mirrors. The build uses the normal Docker network mode after the
daemon-level DNS fix; this changes neither the runtime network configuration
nor the networks attached to the resulting image.

## Database migration gate

Every `prod-*` tag runs `migrate_prod_db` after the registry smoke test and
image build, but before the Swarm service update. The job runs the exact
`macocr-admin migrate` binary from the immutable image digest on the Pi5 Swarm
manager, attached only to the existing `pg-net` overlay network. It receives a
protected `MACOCR_MIGRATION_DATABASE_URL` variable through stdin; the URL is
never written to the repository, image, job log, or a persistent host file.

The application currently uses one idempotent PostgreSQL schema migration
function. The same function is used at application startup and by the explicit
migration command, with a PostgreSQL advisory lock so a release job and a
starting service cannot apply the schema concurrently. A migration failure
blocks deployment. Tags for `dev` and `staging` do not touch production.

New schema changes must follow expand/contract compatibility: add or backfill
before the new binary depends on a change, keep the previous image able to
start during rollback, and defer destructive drops to a separately reviewed
release. The migration job is therefore a release gate, not a second source of
schema definitions.

## GitLab variables

Common variables are provisioned at the `homelab-apps` group scope. Deployment
credentials remain project/environment-scoped unless the lab secret policy
explicitly approves sharing them. Secret variables must be masked and
protected; the protected tag policy is the production gate.

| Variable | Purpose | Secret | Required by |
|---|---|---:|---|
| `LAB_REGISTRY_HOST` | `registry-gitlab.dungxbuif.com` | no | group registry/build |
| `LAB_REGISTRY_PUSH_USER` | write deploy-token username | yes | smoke/build |
| `LAB_REGISTRY_PUSH_PASSWORD` | write deploy-token value | yes | smoke/build |
| `LAB_REGISTRY_PULL_USER` | read deploy-token username | yes | Swarm deploy |
| `LAB_REGISTRY_PULL_PASSWORD` | read deploy-token value | yes | Swarm deploy |
| `MACOCR_MIGRATION_DATABASE_URL` | production PostgreSQL URL for the one-shot migration command | yes | migration gate |
| `MACOCR_SWARM_MANAGER_HOST` | Pi5 Swarm manager address | no | deploy |
| `MACOCR_SWARM_MANAGER_USER` | SSH user on the manager | no | deploy |
| `MACOCR_SWARM_SSH_PRIVATE_KEY` | protected deployment key | yes | deploy |
| `MACOCR_SWARM_KNOWN_HOSTS` | pinned SSH host key line(s) | yes | deploy |

The GitLab full-access PAT is an operator bootstrap credential only. It must
not be placed in CI variables or passed to jobs. Use it to provision the
least-privilege registry deploy tokens and runner, then keep it in the human
secret store.

## Runtime contract preserved by deployment

The pipeline changes only the image of the existing Swarm service. It does not
replace environment variables, mounts, replicas, health checks, or network
attachments. The current service topology is:

- `edge-http-net` for ingress;
- `macocr-redis-net` for Redis;
- `pg-net` for PostgreSQL/pgbouncer;
- one replica, one CPU and one GiB limit, `stop-first` with rollback on update
  failure.

Keep runtime credentials out of the image. The existing service still has a
follow-up migration from plain environment variables to versioned Swarm
secrets; that migration is separate from this first CI/CD delivery and must be
completed with a native-worker compatibility check before rotating the shared
native authentication secret.

## Registry smoke test and rollback

Before building the application image, `registry_smoke` builds a scratch image,
pushes it to the project registry, pulls it back, and verifies a digest. The
smoke tag is unique to the pipeline and is removed locally; registry retention
must clean these CI smoke tags.

The deploy job consumes the digest artifact from the build job and uses
`docker service update --with-registry-auth`. Swarm's existing rollback policy
remains in force. To roll back manually, update the service to the previous
recorded digest; do not reuse a mutable `latest` tag.
