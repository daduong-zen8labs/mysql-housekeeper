#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
go test ./... -count=1
golangci-lint run
go test ./... -coverprofile=coverage.out -covermode=atomic
go tool cover -func=coverage.out | tail -n 1
