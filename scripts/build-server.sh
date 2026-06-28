#!/bin/bash
# Build SafeLink Server Docker image
set -e
cd "$(dirname "$0")/.."
echo "Building SafeLink Server Docker image..."
docker build -f server/Dockerfile -t safelink-server .
echo "Done! Run with: docker compose -f server/docker-compose.yml up"
