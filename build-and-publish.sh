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

# Security warning
echo "⚠️ SECURITY WARNING ⚠️"
echo "Ensure that no sensitive environment variables are included in the Docker image."
echo "Check that .dockerignore is properly configured to exclude .env files."
echo "Continue with build? (y/n)"
read -r response
if [[ "$response" != "y" ]]; then
  echo "Build cancelled."
  exit 0
fi

echo "Building Docker image"
docker-compose build bot

echo "Tagging the image: $IMAGE_TAG"
docker tag docker-bot:latest $IMAGE_TAG

echo "Pushing the image to Docker Hub: $IMAGE_TAG"
docker push $IMAGE_TAG

echo "Docker image $IMAGE_TAG has been successfully published!"
echo "Remember: When running this container, pass environment variables securely using --env-file" 
