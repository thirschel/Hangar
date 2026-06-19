#!/usr/bin/env bash
set -e

cd "$(dirname "$0")"

echo "Building Hangar..."
go build -ldflags "-s -w" -o cs .

echo "Build successful: $(pwd)/cs"
