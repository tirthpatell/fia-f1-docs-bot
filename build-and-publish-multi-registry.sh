#!/bin/bash

# Exit the script if any command fails
set -e

# Script directory
SCRIPT_DIR="$(dirname "$0")"

# Load environment variables from the .env file
if [ -f "$SCRIPT_DIR/.env" ]; then
  export $(grep -v '^#' "$SCRIPT_DIR/.env" | xargs)
else
  echo "Error: .env file not found in $SCRIPT_DIR."
  exit 1
fi

# Check if required variables are set
if [ -z "$DOCKER_USER" ] || [ -z "$DOCKER_REPO" ]; then
  echo "Error: DOCKER_USER or DOCKER_REPO not set in .env file."
  exit 1
fi

# Registry selection
REGISTRIES=()
echo "Select registries to push to:"
echo "1) Docker Hub only"
echo "2) GitHub Container Registry only"
echo "3) Both Docker Hub and GitHub Container Registry"
read -r choice

case $choice in
  1)
    REGISTRIES=("docker")
    ;;
  2)
    REGISTRIES=("ghcr")
    ;;
  3)
    REGISTRIES=("docker" "ghcr")
    ;;
  *)
    echo "Invalid choice. Exiting."
    exit 1
    ;;
esac

# Get GitHub username if needed
if [[ " ${REGISTRIES[@]} " =~ " ghcr " ]]; then
  if [ -z "$GITHUB_USER" ]; then
    echo "Enter your GitHub username (or organization name):"
    read -r GITHUB_USER
  fi
  if [ -z "$GITHUB_USER" ]; then
    echo "Error: GitHub username is required for GHCR."
    exit 1
  fi
fi

# Get the current Git tag (or fallback to "latest" if no tag is found)
VERSION=$(git describe --tags --abbrev=0 2>/dev/null || echo "latest")

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

# Navigate to the docker directory
cd "$SCRIPT_DIR/docker"

# Build the image
echo "Building Docker image..."
docker-compose build bot

# Function to tag and push to a registry
push_to_registry() {
  local registry=$1
  local image_name=$2
  local tag=$3
  
  echo "Tagging image for $registry..."
  docker tag docker-bot:latest "$image_name:$tag"
  
  echo "Pushing to $registry: $image_name:$tag"
  docker push "$image_name:$tag"
  
  # Also push latest tag
  if [ "$tag" != "latest" ]; then
    docker tag docker-bot:latest "$image_name:latest"
    docker push "$image_name:latest"
  fi
}

# Push to selected registries
for registry in "${REGISTRIES[@]}"; do
  case $registry in
    "docker")
      # Docker Hub
      IMAGE_NAME="$DOCKER_USER/$DOCKER_REPO"
      push_to_registry "Docker Hub" "$IMAGE_NAME" "$VERSION"
      ;;
    "ghcr")
      # GitHub Container Registry
      # Login to GHCR if not already logged in
      if ! docker pull ghcr.io/$GITHUB_USER/test 2>&1 | grep -q "pull access denied"; then
        echo "Not logged in to GitHub Container Registry. Please login:"
        echo "You need a GitHub Personal Access Token with 'write:packages' scope"
        echo "Run: docker login ghcr.io -u $GITHUB_USER"
        echo "Then re-run this script."
        exit 1
      fi
      
      GHCR_IMAGE_NAME="ghcr.io/$GITHUB_USER/$DOCKER_REPO"
      push_to_registry "GitHub Container Registry" "$GHCR_IMAGE_NAME" "$VERSION"
      ;;
  esac
done

echo "✅ Docker images have been successfully published!"
echo "Remember: When running this container, pass environment variables securely using --env-file"

# Show published images
echo ""
echo "Published images:"
for registry in "${REGISTRIES[@]}"; do
  case $registry in
    "docker")
      echo "  - Docker Hub: $DOCKER_USER/$DOCKER_REPO:$VERSION"
      echo "  - Docker Hub: $DOCKER_USER/$DOCKER_REPO:latest"
      ;;
    "ghcr")
      echo "  - GHCR: ghcr.io/$GITHUB_USER/$DOCKER_REPO:$VERSION"
      echo "  - GHCR: ghcr.io/$GITHUB_USER/$DOCKER_REPO:latest"
      ;;
  esac
done