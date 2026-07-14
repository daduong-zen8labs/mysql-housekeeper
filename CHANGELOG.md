# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Initial CLI: `plan`, `run`, `version`
- YAML retention policies with optional SQL filters
- Idempotent primary → housekeeping copy-then-delete
- Job checkpoints (`hk_job_runs`, `hk_checkpoints`)
- Docker Compose demo + integration tests
- CI (lint, unit tests, race, coverage, govulncheck, integration)
- GoReleaser config for multi-OS/arch binaries

## [0.1.0] - TBD

First public release (tag when ready).
