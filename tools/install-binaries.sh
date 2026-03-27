#!/bin/bash
set -euo pipefail

# Build dalcenter binaries from the current repo and install them into PREFIX.
# Default target matches the documented host installation path.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
PREFIX="${PREFIX:-/usr/local/bin}"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

cd "$REPO_DIR"

echo "[install-binaries] building dalcenter"
go build -o "$tmpdir/dalcenter" ./cmd/dalcenter

echo "[install-binaries] building dalcli"
go build -o "$tmpdir/dalcli" ./cmd/dalcli

echo "[install-binaries] building dalcli-leader"
go build -o "$tmpdir/dalcli-leader" ./cmd/dalcli-leader

mkdir -p "$PREFIX"
install -m 0755 "$tmpdir/dalcenter" "$PREFIX/dalcenter"
install -m 0755 "$tmpdir/dalcli" "$PREFIX/dalcli"
install -m 0755 "$tmpdir/dalcli-leader" "$PREFIX/dalcli-leader"

echo "[install-binaries] installed to $PREFIX"
ls -l "$PREFIX/dalcenter" "$PREFIX/dalcli" "$PREFIX/dalcli-leader"
