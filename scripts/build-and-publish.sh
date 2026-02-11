#!/bin/bash

# Manually build and push a Docker image to Docker Hub.
# Requires a scripts/.env file with DOCKER_USER and DOCKER_REPO set.

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Load environment variables
if [ -f "$SCRIPT_DIR/.env" ]; then
  export $(grep -v '^#' "$SCRIPT_DIR/.env" | xargs)
else
  echo "Error: .env file not found in $SCRIPT_DIR (see .env.example)."
  exit 1
fi

if [ -z "$DOCKER_USER" ] || [ -z "$DOCKER_REPO" ]; then
  echo "Error: DOCKER_USER or DOCKER_REPO not set in .env file."
  exit 1
fi

IMAGE_NAME="$DOCKER_USER/$DOCKER_REPO"

echo "Building Docker image: $IMAGE_NAME:latest (linux/amd64)"

# Setup buildx builder
docker buildx create --name multiplatform --use 2>/dev/null || docker buildx use multiplatform

# Build from project root so paths are straightforward
docker buildx build \
  --platform linux/amd64 \
  --file "$PROJECT_ROOT/docker/DOCKERFILE" \
  --tag "$IMAGE_NAME:latest" \
  --load \
  "$PROJECT_ROOT/bot"

echo "Pushing: $IMAGE_NAME:latest"
docker push "$IMAGE_NAME:latest"

echo "Done."
