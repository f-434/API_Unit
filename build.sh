#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"

echo "==> frontend"
cd frontend
npm install
npm run build
cd ..

echo "==> backend tests"
cd backend
go test ./...
cd ..

echo "==> backend build"
cd backend
go build -o ../apiunit ./cmd/server
cd ..

echo
echo "Build complete: ./apiunit"
echo "Run:  DATA_DIR=./backend/data ./apiunit"
echo "      (WEB_DIR is auto-detected as backend/web)"
