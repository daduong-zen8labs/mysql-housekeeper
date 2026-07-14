# mysql-housekeeper

CLI for **MySQL 8+** database housekeeping: move expired rows from a **primary** database to a **housekeeping** (archive) database using per-table YAML retention policies.

## How it works

For each configured table:

1. Select expired rows (`time_column < now_utc - retention` [AND optional filter]), paginated by primary key.
2. `INSERT IGNORE` into the housekeeping database (idempotent).
3. Verify rows exist in housekeeping.
4. `DELETE` those primary keys from the primary database.
5. Persist a checkpoint on the housekeeping DB (`hk_checkpoints`).

Cross-database XA is **not** used. Safety comes from idempotent copy-then-delete: if the process crashes after insert and before delete, the next run inserts (no-op on duplicates) and deletes again. **At-least-once move, no data loss.**

Cutoff times use **UTC** (`SET time_zone = '+00:00'`).

## Requirements

- MySQL **≥ 8.0** on both primary and housekeeping
- Tables must have a **PRIMARY KEY** (composite keys supported)
- Go 1.22+ to build from source

## Install

```bash
go install github.com/nudgeworks/mysql-housekeeper/cmd/mysql-housekeeper@latest
```

Or build locally:

```bash
go build -o mysql-housekeeper ./cmd/mysql-housekeeper
```

Docker:

```bash
docker build -t mysql-housekeeper .
docker run --rm mysql-housekeeper version
```

## Quickstart (docker compose)

### 1. Create / start local MySQL databases

From the repo root:

```bash
# create & start primary + housekeeping MySQL 8 containers
docker compose up -d --wait

# check status
docker compose ps
```

| Role | Host port | Database | User | Password |
|------|-----------|----------|------|----------|
| Primary | `127.0.0.1:13306` | `app` | `housekeeper` | `housekeeper` |
| Housekeeping | `127.0.0.1:13307` | `archive` | `housekeeper` | `housekeeper` |

Ports **13306** / **13307** avoid clashing with a local MySQL on 3306. Seed data is loaded from `docker/primary-init.sql` on first start.

Optional — connect with the MySQL client:

```bash
mysql -h 127.0.0.1 -P 13306 -u housekeeper -phousekeeper app
mysql -h 127.0.0.1 -P 13307 -u housekeeper -phousekeeper archive
```

Reset demo data (wipe volumes and recreate):

```bash
docker compose down -v
docker compose up -d --wait
```

### 2. Run housekeeper against the demo DBs

```bash
# 1) Count expired rows (no writes)
go run ./cmd/mysql-housekeeper plan -c configs/demo.yaml

# 2) Dry run: scan batches but do NOT insert/delete
go run ./cmd/mysql-housekeeper run -c configs/demo.yaml --dry-run

# 3) Real move: copy to archive, then delete from primary
go run ./cmd/mysql-housekeeper run -c configs/demo.yaml

# 4) Re-run — idempotent; ~0 rows if everything already moved
go run ./cmd/mysql-housekeeper run -c configs/demo.yaml
```

Stop when done:

```bash
docker compose down        # keep data volumes
docker compose down -v     # also delete data volumes
```

## Config

See `configs/example.yaml`:

```yaml
primary:
  dsn: "${PRIMARY_DSN}"
housekeeping:
  dsn: "${HOUSEKEEPING_DSN}"
defaults:
  batch_size: 1000
  max_rows_per_run: 500000
  dry_run: false
  throttle_ms: 0
tables:
  - name: notification_logs
    time_column: created_at
    retention: 90d          # Nd, Nh, Nm, Ns
    filter: "status IN ('sent','failed')"  # optional; no leading WHERE; no ';'
    # primary_key: [id]     # optional override; otherwise discovered via INFORMATION_SCHEMA
```

`${ENV_VAR}` placeholders in the YAML are expanded from the environment.

**Table order matters** when foreign keys exist: list child tables before parents. The tool does **not** disable `foreign_key_checks`.

## CLI

```
mysql-housekeeper run  -c housekeeper.yaml [--dry-run] [--table name]
mysql-housekeeper plan -c housekeeper.yaml [--table name]
mysql-housekeeper version
```

| Exit code | Meaning |
|-----------|---------|
| 0 | success |
| 1 | runtime error |
| 2 | config / validation error |

`plan` only estimates row counts (no data writes).  
`run --dry-run` selects batches and logs counts without insert/delete (does not create archive tables).

Structured JSON logs go to stdout; final run summary is also JSON.

## Scheduling

Run under any scheduler that can execute a container or binary periodically:

**cron**

```cron
0 3 * * * PRIMARY_DSN=... HOUSEKEEPING_DSN=... /usr/local/bin/mysql-housekeeper run -c /etc/housekeeper.yaml
```

**Kubernetes CronJob** — mount the config ConfigMap and set DSNs via Secrets as env vars.

**ECS scheduled task** — same pattern: task role + secrets for DSNs, invoke `run -c ...`.

## Schema on housekeeping

If the target table is missing, the tool recreates it from the primary `SHOW CREATE TABLE` output. If it exists, column types and primary key must match; otherwise the run fails with a schema-drift error.

State tables created on housekeeping:

- `hk_job_runs`
- `hk_checkpoints`

## Tests

```bash
go test ./...
# with compose MySQL running:
go test -tags=integration ./internal/mover/ -count=1 -v
```

## Roadmap

- Partition exchange / detach mode
- Purge / second-tier retention on housekeeping
- Prometheus metrics endpoint

## License

Apache License 2.0 — see [LICENSE](LICENSE).
