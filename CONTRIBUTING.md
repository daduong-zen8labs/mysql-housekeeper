# Contributing

Thanks for contributing to **mysql-housekeeper**.

## Development setup

Requirements:

- Go **1.22+**
- Docker (for integration tests / demo MySQL)

```bash
git clone https://github.com/nudgeworks/mysql-housekeeper.git
cd mysql-housekeeper
go test ./...
```

## Coding conventions

- Format with `gofmt` (CI enforces `gofmt` via golangci-lint)
- Prefer `fmt.Errorf("...: %w", err)` for wrapping
- Keep packages under `internal/` unless something is intentionally public
- No secrets in commits; use `${ENV}` placeholders in sample configs
- Demo credentials in `docker-compose.yml` / `configs/demo.yaml` are **local demo only**

## Tests

```bash
# unit tests
go test ./...

# with the race detector (requires CGO on some platforms)
go test -race ./...

# coverage
go test ./... -coverprofile=coverage.out
go tool cover -func=coverage.out

# integration (needs compose MySQL)
docker compose up -d --wait
go test -tags=integration ./internal/mover/ -count=1 -v
docker compose down -v
```

Lint locally (optional):

```bash
golangci-lint run
```

## Pull requests

1. Open an issue first for larger design changes
2. Keep PRs focused and small when possible
3. Include / update tests for behavior changes
4. Update `CHANGELOG.md` under `[Unreleased]`
5. Ensure CI is green

## Release notes

Maintainers cut releases with Git tags (`vMAJOR.MINOR.PATCH`) and GoReleaser.
See [SECURITY.md](SECURITY.md) for vulnerability reports.
