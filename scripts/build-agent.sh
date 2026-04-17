#!/bin/bash
set -e

VERSION=${1:-dev}
OUT=dist

mkdir -p $OUT

echo "Building agent v$VERSION..."

GOOS=darwin  GOARCH=arm64 go build -ldflags="-X main.version=$VERSION" -o $OUT/revtunnel-darwin-arm64     ./cmd/agent
GOOS=darwin  GOARCH=amd64 go build -ldflags="-X main.version=$VERSION" -o $OUT/revtunnel-darwin-amd64     ./cmd/agent
GOOS=linux   GOARCH=amd64 go build -ldflags="-X main.version=$VERSION" -o $OUT/revtunnel-linux-amd64      ./cmd/agent
GOOS=windows GOARCH=amd64 go build -ldflags="-X main.version=$VERSION" -o $OUT/revtunnel-windows-amd64.exe ./cmd/agent

echo "Done:"
ls -lh $OUT/