#!/usr/bin/env bash
# Regenerates the API types from openapi.yaml:
#
#   1. oapi-codegen writes internal/generated/types.go (types only).
#   2. internal/cmd/genexports writes types_gen.go, which re-exports them
#      from package invoist.
#
# Both outputs are committed; `generate` must leave no diff. The spec is
# testdata/openapi.yaml; set INVOIST_OPENAPI to read another.
set -euo pipefail
cd "$(dirname "$0")/.."

OAPI_CODEGEN_VERSION=v2.8.2
spec="${INVOIST_OPENAPI:-testdata/openapi.yaml}"
[[ -f "$spec" ]] || { echo "generate: no OpenAPI spec at $spec (set INVOIST_OPENAPI)" >&2; exit 1; }

go run "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@${OAPI_CODEGEN_VERSION}" \
  -config internal/generated/config.yaml "$spec"
go run ./internal/cmd/genexports
