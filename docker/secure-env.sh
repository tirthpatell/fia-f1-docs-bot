#!/bin/bash

# This script demonstrates how to securely pass environment variables to Docker containers
# without including them in the image

# Example of running a container with environment variables:
# docker run --env-file /path/to/your/.env -p 8080:8080 your-image-name

echo "To run your container securely with environment variables:"
echo "docker run --env-file /path/to/your/.env -p 8080:8080 your-image-name"
echo ""
echo "For docker-compose:"
echo "docker-compose --env-file /path/to/your/.env up"
echo ""
echo "NEVER include .env files in your Docker images!"
echo "NEVER commit .env files with real credentials to version control!" 
