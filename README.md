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
- **Docker Support**: Easily deployable using Docker for consistent environments.

## How It Works

1. **Scraping**: The bot scrapes the FIA website at a user-defined interval to check for new decision documents.
2. **Processing**: New documents are converted to images and summarized using Google Gemini API.
3. **Image Hosting**: Images are uploaded to a Picsur instance to get public URLs.
4. **Posting**: The bot automatically posts the images with AI summaries to the configured Threads account.

## Requirements

- Docker and Docker Compose (for containerized deployment)
- Go 1.23+ (for local development)
- Threads API access (see Limitations section)
- Google Gemini API for AI summarization (Can be skipped if not needed)
- Picsur instance for image hosting (self-hosted or third-party)

## Quick Start with Docker

1. Pull the Docker image:
   ```sh
   docker pull ptirth/fia-f1-docs-bot:latest
   ```

2. Create a `.env` file with the following variables:
   ```
   DOCUMENT=file.json
   FIA_URL=https://www.fia.com/documents/championships/fia-formula-one-world-championship-14/season/season-2024-2043
   SCRAPE_INTERVAL=300
   THREADS_USER_ID=your_threads_user_id_here
   THREADS_ACCESS_TOKEN=your_threads_access_token_here
   GEMINI_API_KEY=your_google_gemini_api_key_here
   PICSUR_API=your_picsur_api_key_here
   PICSUR_URL=https://picsur.example.com
   ```

3. Create a `docker-compose.yml` file:
   ```yaml
   services:
     bot:
       image: ptirth/fia-f1-docs-bot:latest
       env_file: .env
       volumes:
         - ./data:/app/data
       restart: unless-stopped
   ```

4. Run the bot:
   ```sh
   docker-compose up -d
   ```

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

## Contributing

Contributions are welcome! Here's how you can contribute to the project:

1. Fork the repository
2. Create a new branch: `git checkout -b feature-branch-name`
3. Make your changes and commit them: `git commit -m 'Add some feature'`
4. Push to the branch: `git push origin feature-branch-name`
5. Submit a pull request

## Security

If you discover any security-related issues, please email [fiaf1docs-gh@threadsutil.cc] instead of using the issue tracker. All security vulnerabilities will be promptly addressed.

## Limitations

- This bot relies on the Threads API, which may have rate limits or require special access. Ensure you have the necessary permissions before deploying.
- The bot's functionality is dependent on the structure of the FIA website. Changes to their website may require updates to the scraping logic.

## License

This project is licensed under the MIT License. See the `LICENSE` file for details.
