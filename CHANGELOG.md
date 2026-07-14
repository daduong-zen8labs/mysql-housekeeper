# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-07-14

### Added

- Initial CLI: `plan`, `run`, `version`
- YAML retention policies with optional SQL filters
- Idempotent primary → housekeeping copy-then-delete
- Job checkpoints (`hk_job_runs`, `hk_checkpoints`)
- Docker Compose demo + integration tests
- CI (lint, unit tests, race, coverage, govulncheck, Trivy, integration)
- GoReleaser config for multi-OS/arch binaries
- OSS hygiene: CONTRIBUTING, CODE_OF_CONDUCT, SECURITY, Dependabot

[Unreleased]: https://github.com/daduong-zen8labs/mysql-housekeeper/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/daduong-zen8labs/mysql-housekeeper/releases/tag/v0.1.0
