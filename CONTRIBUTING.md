# Contributing

Thanks for contributing to **mysql-housekeeper**.

## Development setup

Requirements:

- Go **1.22+**
- Docker (for integration tests / demo MySQL)

```bash
git clone https://github.com/daduong-zen8labs/mysql-housekeeper.git
cd mysql-housekeeper
go test ./...
```

For race, coverage, and integration commands, see [README → Tests](README.md#tests).

## Coding conventions

- Format with `gofmt` (CI enforces `gofmt` via golangci-lint)
- Prefer `fmt.Errorf("...: %w", err)` for wrapping
- Keep packages under `internal/` unless something is intentionally public
- No secrets in commits; use `${ENV}` placeholders in sample configs
- Demo credentials in `docker-compose.yml` / `configs/demo.yaml` are **local demo only**

Optional local lint:

```bash
golangci-lint run
# or
./scripts/check.sh
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
