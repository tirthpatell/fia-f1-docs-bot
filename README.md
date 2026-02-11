![svgviewer-output (1)](https://github.com/user-attachments/assets/af6d1f59-a3e7-44a4-9e53-b82ded41d5ff)

# FIA F1 Docs Bot

The FIA F1 Docs Bot is an automated tool designed to fetch the latest Formula 1 decision documents from the FIA website and post them on a Threads account with AI summarization.

## ⚠️ Disclaimer

**IMPORTANT NOTICE:** This project is not affiliated with, endorsed by, or in any way officially connected to the Fédération Internationale de l'Automobile (FIA), Formula 1, Formula One Management (FOM), or any of their subsidiaries or affiliates. This is an independent, unofficial project created for informational purposes only. All product and company names are the registered trademarks of their original owners. The use of any trade name or trademark is for identification and reference purposes only and does not imply any association with the trademark holder of their product brand.

## About

This bot aims to make FIA F1 decision documents more accessible by automatically posting them to a Threads account with AI summarization. It's designed for F1 fans and professionals who want quick access to official FIA communications.

## Features

- **Automated Scraping**: Periodically scrapes the FIA website for the latest decision documents.
- **Automated Posting**: Posts the fetched documents to a specified Threads account.
- **AI Summarization**: Uses Google Gemini to generate concise summaries of each document.
- **URL Shortening**: Shortens document URLs to include them in posts without exceeding character limits.
- **Docker Support**: Easily deployable using Docker for consistent environments.

## How It Works

1. **Scraping**: The bot scrapes the FIA website at a user-defined interval to check for new decision documents.
2. **Processing**: New documents are converted to images and summarized using Google Gemini API.
3. **Image Hosting**: Images are uploaded to a Picsur instance to get public URLs.
4. **URL Shortening**: Document URLs are shortened to fit within Threads character limits.
5. **Posting**: The bot automatically posts the images with AI summaries and shortened URLs to the configured Threads account.

## Requirements

- Docker and Docker Compose (for containerized deployment)
- Go 1.23+ (for local development)
- Threads API access (see Limitations section)
- Google Gemini API for AI summarization (Can be skipped if not needed)
- Picsur instance for image hosting (self-hosted or third-party)
- URL shortener service for document links

## Technical Implementation

This bot uses the [Threads Go client library](https://pkg.go.dev/github.com/tirthpatell/threads-go) to interact with the Threads API, providing a clean and efficient way to post content to Threads without handling raw HTTP requests.

## Quick Start with Docker

### Using Docker Hub

1. Pull the Docker image from Docker Hub:
   ```sh
   docker pull ptirth/fia-f1-docs-bot:latest
   ```

### Using GitHub Container Registry (GHCR)

1. Pull the Docker image from GitHub Container Registry:
   ```sh
   docker pull ghcr.io/tirthpatell/fia-f1-docs-bot:latest
   ```

2. Create a `.env` file with the following variables:
   ```
   FIA_URL=https://www.fia.com/documents/championships/fia-formula-one-world-championship-14/season/season-2025-2071
   SCRAPE_INTERVAL=300
   
   # Threads API Configuration (required for Threads Go client)
   THREADS_USER_ID=your_threads_user_id_here
   THREADS_ACCESS_TOKEN=your_threads_access_token_here
   THREADS_CLIENT_ID=your_threads_client_id_here
   THREADS_CLIENT_SECRET=your_threads_client_secret_here
   THREADS_REDIRECT_URI=your_threads_redirect_uri_here
   
   # AI and External Services
   GEMINI_API_KEY=your_google_gemini_api_key_here
   PICSUR_API=your_picsur_api_key_here
   PICSUR_URL=https://picsur.example.com
   SHORTENER_API_KEY=your_shortener_api_key_here
   SHORTENER_URL=https://shortener.example.com
   
   # PostgreSQL Configuration
   DB_HOST=localhost
   DB_PORT=5432
   DB_USER=postgres
   DB_PASSWORD=your_secure_password
   DB_NAME=fiadocs
   DB_SSL_MODE=disable
   
   ```

3. Run the container:
   ```sh
   docker run -d \
     --name fia-f1-docs-bot \
     --env-file .env \
     ptirth/fia-f1-docs-bot:latest
   ```
   
   Or using Docker Compose:
   ```yaml
   services:
     bot:
       image: ptirth/fia-f1-docs-bot:latest  # or ghcr.io/tirthpatell/fia-f1-docs-bot:latest
       env_file:
         - .env
       restart: unless-stopped
       ports:
         - "6060:6060"  # Health check endpoint
   ```

### Persistent Storage

The bot uses PostgreSQL to store information about processed documents, ensuring persistence across container restarts and deployments.

### PostgreSQL Configuration

To configure PostgreSQL:

1. Set up a PostgreSQL database
2. Configure the environment variables as shown in the Quick Start section
3. The bot will automatically create the necessary tables in the database

## Building and Publishing Docker Images

### Manual Build to Multiple Registries

To build and publish Docker images to both Docker Hub and GitHub Container Registry:

1. **Set up authentication:**
   
   For Docker Hub:
   ```sh
   docker login
   ```
   
   For GitHub Container Registry:
   ```sh
   # Create a Personal Access Token with 'write:packages' scope
   docker login ghcr.io -u YOUR_GITHUB_USERNAME
   ```

2. **Run the multi-registry build script:**
   ```sh
   ./build-and-publish-multi-registry.sh
   ```
   
   The script will prompt you to select which registries to push to.

### Automated Builds with GitHub Actions

This repository includes a GitHub Actions workflow that automatically builds and publishes Docker images to both Docker Hub and GitHub Container Registry when:
- Tags are pushed (e.g., `v1.0.0`)
- Commits are pushed to the `main` branch
- Pull requests are opened (build only, no push)

**Required GitHub Secrets:**
- `DOCKER_USERNAME`: Your Docker Hub username
- `DOCKER_TOKEN`: Your Docker Hub access token

**Automatic Authentication:**
- `GITHUB_TOKEN`: Automatically provided by GitHub Actions for GHCR authentication
- No manual configuration needed for GitHub Container Registry

The workflow uses `${{ secrets.GITHUB_TOKEN }}` which is automatically available in every GitHub Actions workflow with the necessary permissions to push to your repository's container registry.

## Local Development Setup

1. Clone the repository:
   ```sh
   git clone https://github.com/tirthpatell/fia-f1-docs-bot.git
   cd fia-f1-docs-bot
   ```

2. Install dependencies:
   ```sh
   go mod download
   ```

3. Configure the bot:
   - Create a `.env` file in the project root with the necessary environment variables (see Docker setup for required variables).

4. Build the project:
   ```sh
   go build -o bot ./cmd/svc/main.go
   ```

5. Run the bot:
   ```sh
   ./bot
   ```

## Configuration

The bot is configured using environment variables. See the Quick Start section for the required variables.

### URL Shortener Configuration

The bot uses a URL shortener service to create shortened links to the original FIA documents. This allows the bot to include direct links in posts without exceeding Threads' character limits.

Required environment variables for the URL shortener:
- `SHORTENER_API_KEY`: API key for authentication with the shortener service
- `SHORTENER_URL`: Base URL of the shortener service (e.g., https://shortener.example.com)

The shortened URLs will be included in the post text, allowing users to easily access the original documents.

## Contributing

Contributions are welcome! Here's how you can contribute to the project:

1. Fork the repository
2. Create a new branch: `git checkout -b feature-branch-name`
3. Make your changes and commit them: `git commit -m 'Add some feature'`
4. Push to the branch: `git push origin feature-branch-name`
5. Submit a pull request

## Dependencies

This project uses several key dependencies:

- **[Threads Go Client](https://pkg.go.dev/github.com/tirthpatell/threads-go)**: Official Go client library for interacting with the Threads API
- **[Colly](https://github.com/gocolly/colly)**: Web scraping framework for Go
- **[Google Generative AI](https://github.com/google/generative-ai-go)**: Go client for Google's Generative AI (Gemini)
- **[go-fitz](https://github.com/gen2brain/go-fitz)**: PDF processing library for Go

## Security

If you discover any security-related issues, please email [fiaf1docs-gh@threadsutil.cc] instead of using the issue tracker. All security vulnerabilities will be promptly addressed.

## Limitations

- This bot relies on the Threads API, which may have rate limits or require special access. Ensure you have the necessary permissions before deploying.
- The bot's functionality is dependent on the structure of the FIA website. Changes to their website may require updates to the scraping logic.

## License

This project is licensed under the MIT License. See the `LICENSE` file for details.
