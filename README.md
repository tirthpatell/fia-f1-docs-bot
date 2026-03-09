![svgviewer-output (1)](https://github.com/user-attachments/assets/af6d1f59-a3e7-44a4-9e53-b82ded41d5ff)

# FIA F1 Docs Bot

The FIA F1 Docs Bot is an automated tool designed to fetch the latest Formula 1 decision documents from the FIA website and post them on a Threads account with AI summarization.

## Disclaimer

**IMPORTANT NOTICE:** This project is not affiliated with, endorsed by, or in any way officially connected to the Fédération Internationale de l'Automobile (FIA), Formula 1, Formula One Management (FOM), or any of their subsidiaries or affiliates. This is an independent, unofficial project created for informational purposes only. All product and company names are the registered trademarks of their original owners. The use of any trade name or trademark is for identification and reference purposes only and does not imply any association with the trademark holder of their product brand.

## About

This bot aims to make FIA F1 decision documents more accessible by automatically posting them to a Threads account with AI summarization. It's designed for F1 fans and professionals who want quick access to official FIA communications.

## Features

- **Automated Scraping**: Periodically scrapes the FIA website for the latest decision documents under the active Grand Prix.
- **Automated Posting**: Posts documents to Threads as image posts or carousels (up to 20 pages).
- **AI Summarization**: Uses Google Gemini (via Vertex AI) with model fallback to generate concise summaries.
- **URL Shortening**: Shortens document URLs to fit within Threads character limits.
- **Recalled Document Detection**: Detects recalled documents and posts text-only notices.
- **Concurrent Processing**: Worker pool processes up to 5 documents concurrently.
- **Health Check**: HTTP endpoint on port 6060 for monitoring.
- **Automatic Token Refresh**: Background goroutine refreshes Threads access token every 24 hours.
- **Graceful Shutdown**: Handles SIGINT/SIGTERM with proper cleanup.
- **Docker Support**: Multi-stage Docker build for easy deployment.

## How It Works

1. **Scraping**: The bot scrapes the FIA website at a configurable interval (default 30s) for new decision documents under the currently active Grand Prix.
2. **Duplicate Check**: New documents are checked against PostgreSQL to skip already-processed ones.
3. **Recall Check**: Documents with "Recalled" in the title get a text-only notice posted instead.
4. **PDF Download & Verification**: PDFs are downloaded and verified (valid PDF signature, >1KB file size).
5. **AI Summary**: The PDF is sent to Google Gemini via Vertex AI for a 40-60 word summary. If summarization fails, posting continues without a summary.
6. **Image Conversion**: PDF pages are converted to images using MuPDF (via go-fitz).
7. **Image Upload**: Images are uploaded to a Picsur instance to get public URLs.
8. **URL Shortening**: Document URLs are shortened to fit within character limits.
9. **Posting**: The bot posts to Threads — single image post for 1-page documents, carousel for multi-page (up to 20).
10. **Cleanup**: Temporary files are deleted and garbage collection is forced after processing.

## Requirements

- Docker and Docker Compose (for containerized deployment)
- Go 1.25+ (for local development)
- MuPDF system libraries (for PDF-to-image conversion)
- Threads API access (see Limitations section)
- Google Gemini API key (via Vertex AI)
- Picsur instance for image hosting (self-hosted or third-party)
- URL shortener service for document links
- PostgreSQL database

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
   FIA_URL=https://www.fia.com/documents/championships/fia-formula-one-world-championship-14/season/season-2026-2072
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
     -p 6060:6060 \
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

The bot uses PostgreSQL to store information about processed documents, ensuring persistence across container restarts and deployments. Tables are automatically created and migrated on startup.

## Building and Publishing Docker Images

### GitHub Actions (CI/CD)

Docker images are automatically built and published to both Docker Hub and GitHub Container Registry on:
- Pushes to `main`
- Tag pushes (e.g., `v1.0.0`)

**Required GitHub Secrets:**
- `DOCKER_USERNAME`: Your Docker Hub username
- `DOCKER_TOKEN`: Your Docker Hub access token
- `GITHUB_TOKEN`: Automatically provided for GHCR authentication

### Claude Code Review

Pull requests are automatically reviewed by Claude Code for code quality, correctness, and security issues. This runs on PR creation and subsequent pushes.

**Required GitHub Secret:**
- `ANTHROPIC_API_KEY`: Your Anthropic API key

### Manual Build

For manual builds outside of CI, use the script in `scripts/`:
```sh
./scripts/build-and-publish.sh
```

This requires a `scripts/.env` file with `DOCKER_USER` and `DOCKER_REPO` set (see `scripts/.env.example`).

## Local Development Setup

1. Clone the repository:
   ```sh
   git clone https://github.com/tirthpatell/fia-f1-docs-bot.git
   cd fia-f1-docs-bot/bot
   ```

2. Install dependencies:
   ```sh
   go mod download
   ```

3. Configure the bot:
   - Create a `.env` file in the `bot/` directory with the necessary environment variables (see Docker setup for required variables).

4. Build the project:
   ```sh
   go build -o bot ./cmd/svc/main.go
   ```

5. Run the bot:
   ```sh
   ./bot
   ```

## Configuration

The bot is configured using environment variables, loaded from a `.env` file via Viper.

| Variable | Required | Default | Description |
|---|---|---|---|
| `FIA_URL` | No | 2025 season URL | FIA documents page URL |
| `SCRAPE_INTERVAL` | No | `30` | Scraping interval in seconds |
| `THREADS_ACCESS_TOKEN` | Yes | | Threads API access token |
| `THREADS_USER_ID` | Yes | | Threads user ID |
| `THREADS_CLIENT_ID` | Yes | | Threads OAuth client ID |
| `THREADS_CLIENT_SECRET` | Yes | | Threads OAuth client secret |
| `THREADS_REDIRECT_URI` | Yes | | Threads OAuth redirect URI |
| `GEMINI_API_KEY` | Yes | | Google Gemini API key |
| `PICSUR_API` | Yes | | Picsur API key |
| `PICSUR_URL` | Yes | | Picsur instance URL |
| `SHORTENER_API_KEY` | Yes | | URL shortener API key |
| `SHORTENER_URL` | Yes | | URL shortener base URL |
| `DB_HOST` | Yes | | PostgreSQL host |
| `DB_PORT` | No | `5432` | PostgreSQL port |
| `DB_USER` | Yes | | PostgreSQL user |
| `DB_PASSWORD` | Yes | | PostgreSQL password |
| `DB_NAME` | Yes | | PostgreSQL database name |
| `DB_SSL_MODE` | No | `disable` | PostgreSQL SSL mode |
| `LOG_LEVEL` | No | `info` | Log level (debug, info, warn, error) |
| `LOG_ADD_SOURCE` | No | `false` | Include source location in logs |
| `ENVIRONMENT` | No | `production` | Environment name |
| `VERSION` | No | `unknown` | Application version |

## Contributing

Contributions are welcome! Here's how you can contribute to the project:

1. Fork the repository
2. Create a new branch: `git checkout -b feature-branch-name`
3. Make your changes and commit them: `git commit -m 'Add some feature'`
4. Push to the branch: `git push origin feature-branch-name`
5. Submit a pull request

## Dependencies

This project uses several key dependencies:

- **[Threads Go Client](https://pkg.go.dev/github.com/tirthpatell/threads-go)**: Go client library for interacting with the Threads API
- **[Colly](https://github.com/gocolly/colly)**: Web scraping framework for Go
- **[Google GenAI](https://pkg.go.dev/google.golang.org/genai)**: Go client for Google Gemini via Vertex AI
- **[go-fitz](https://github.com/gen2brain/go-fitz)**: MuPDF wrapper for PDF-to-image conversion
- **[Viper](https://github.com/spf13/viper)**: Configuration management
- **[lib/pq](https://github.com/lib/pq)**: PostgreSQL driver for Go

## Security

If you discover any security-related issues, please email [fiaf1docs-gh@threadsutil.cc] instead of using the issue tracker. All security vulnerabilities will be promptly addressed.

## Limitations

- This bot relies on the Threads API, which may have rate limits or require special access. Ensure you have the necessary permissions before deploying.
- The bot's functionality is dependent on the structure of the FIA website. Changes to their website may require updates to the scraping logic.
- PDF-to-image conversion requires MuPDF system libraries (`libmupdf-dev` for building, `mupdf` and `mupdf-tools` at runtime).

## License

This project is licensed under the MIT License. See the `LICENSE` file for details.
