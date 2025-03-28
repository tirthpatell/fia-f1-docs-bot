#!/bin/bash

# Exit the script if any command fails
set -e

# Load environment variables from the .env file located in the scripts folder
SCRIPT_DIR="$(dirname "$0")"
if [ -f "$SCRIPT_DIR/.env" ]; then
  export $(grep -v '^#' "$SCRIPT_DIR/.env" | xargs)
else
  echo "Error: .env file not found in $SCRIPT_DIR."
  exit 1
fi

# Check if DOCKER_USER and DOCKER_REPO are set
if [ -z "$DOCKER_USER" ] || [ -z "$DOCKER_REPO" ]; then
  echo "Error: DOCKER_USER or DOCKER_REPO not set in .env file."
  exit 1
fi

# Define the full image name
IMAGE_NAME="$DOCKER_USER/$DOCKER_REPO"

# Get the current Git tag (or fallback to "latest" if no tag is found)
VERSION=$(git describe --tags --abbrev=0 2>/dev/null || echo "latest")

# Define the full image tag
IMAGE_TAG="$IMAGE_NAME:$VERSION"

# Navigate to the directory where the docker-compose.yml file is located
cd "$SCRIPT_DIR/../docker"

echo "Building Docker image with tag: $IMAGE_TAG"
docker-compose build bot

echo "Tagging the image: $IMAGE_TAG"
# Get the image ID of the bot service
IMAGE_ID=$(docker-compose images -q bot)
if [ -z "$IMAGE_ID" ]; then
  echo "Error: Could not get image ID for bot service."
  exit 1
fi

docker tag $IMAGE_ID $IMAGE_TAG

echo "Pushing the image to Docker Hub: $IMAGE_TAG"
docker push $IMAGE_TAG

echo "Docker image $IMAGE_TAG has been successfully published!"
