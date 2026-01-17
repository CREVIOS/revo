# TechyBot

A self-hosted GitHub code review bot powered by Claude AI. TechyBot provides intelligent code reviews directly in your pull requests, similar to Cursor's BugBot.

## Features

- **Multiple Review Modes**: Choose from different review styles based on your needs
  - `@techy review` - Standard comprehensive code review
  - `@techy hunt` - Quick bug detection (like BugBot)
  - `@techy security` - Security-focused analysis
  - `@techy performance` - Performance optimization suggestions
  - `@techy analyze` - Deep technical analysis

- **Uses Your Claude Subscription**: Leverages your existing Claude Max/Pro subscription via OAuth
- **Self-Hosted**: Full control over your data and deployment
- **Docker Ready**: Easy deployment with Docker and docker-compose

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         GitHub                                   │
│  ┌──────────────┐    ┌──────────────┐    ┌──────────────┐       │
│  │  PR Comment  │ → │   Webhook    │ → │ Your Server   │       │
│  │ @techy review│    │   Event      │    │  (Docker)    │       │
│  └──────────────┘    └──────────────┘    └──────┬───────┘       │
└──────────────────────────────────────────────────│───────────────┘
                                                   │
┌──────────────────────────────────────────────────▼───────────────┐
│                      TechyBot Server (Go)                        │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────────┐  │
│  │  Webhook    │  │   OAuth     │  │    Claude API Client    │  │
│  │  Handler    │→ │   Manager   │→ │  (anthropic-sdk-go)     │  │
│  └─────────────┘  └─────────────┘  └───────────┬─────────────┘  │
│                                                 │                 │
│  ┌─────────────┐  ┌─────────────┐              │                 │
│  │   GitHub    │  │   Review    │←─────────────┘                 │
│  │   Client    │← │   Modes     │                                │
│  └─────────────┘  └─────────────┘                                │
└──────────────────────────────────────────────────────────────────┘
```

## Quick Start

### 1. Create a GitHub App

1. Go to https://github.com/settings/apps/new
2. Fill in the details:
   - **GitHub App name**: TechyBot (or your preferred name)
   - **Homepage URL**: Your server URL
   - **Webhook URL**: `https://your-server.com/webhook`
   - **Webhook secret**: Generate a secure random string
3. Set permissions:
   - **Contents**: Read
   - **Issues**: Read & Write
   - **Pull requests**: Read & Write
   - **Metadata**: Read
4. Subscribe to events:
   - Issue comment
   - Pull request
   - Pull request review comment
5. Generate and download a private key
6. Note your App ID

### 2. Get Claude OAuth Credentials

TechyBot uses your Claude Max/Pro subscription. Get your credentials from:

```bash
cat ~/.claude/.credentials.json
```

This file contains:
- `accessToken`
- `refreshToken`
- `expiresAt`

### 3. Configure Environment

```bash
cp .env.example .env
```

Edit `.env` with your values:

```bash
# GitHub App
GITHUB_APP_ID=123456
GITHUB_WEBHOOK_SECRET=your-webhook-secret

# Claude OAuth
CLAUDE_ACCESS_TOKEN=sk-ant-oat01-...
CLAUDE_REFRESH_TOKEN=sk-ant-ort01-...
CLAUDE_EXPIRES_AT=1748658860401

# Bot settings
BOT_USERNAME=techy
```

Copy your GitHub App private key:

```bash
cp /path/to/your-app.private-key.pem ./github-private-key.pem
```

### 4. Run with Docker

```bash
docker-compose up --build
```

### 5. Install the GitHub App

1. Go to your GitHub App settings
2. Click "Install App"
3. Select the repositories where you want TechyBot

### 6. Test It

Create a pull request and comment:

```
@techy review
```

## Usage

### Commands

| Command | Description |
|---------|-------------|
| `@techy review` | Standard comprehensive code review |
| `@techy hunt` | Quick bug detection mode |
| `@techy security` | Security-focused analysis |
| `@techy performance` | Performance optimization |
| `@techy analyze` | Deep technical analysis |

Add `verbose` for more detailed output:

```
@techy review verbose
```

### Reactions

TechyBot uses emoji reactions to show status:
- 👀 Processing your request
- 🚀 Review posted successfully
- 😕 An error occurred

## Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `GITHUB_APP_ID` | Your GitHub App ID | Required |
| `GITHUB_WEBHOOK_SECRET` | Webhook secret | Required |
| `GITHUB_PRIVATE_KEY_PATH` | Path to private key | `/app/private-key.pem` |
| `CLAUDE_ACCESS_TOKEN` | OAuth access token | Required |
| `CLAUDE_REFRESH_TOKEN` | OAuth refresh token | Required |
| `CLAUDE_EXPIRES_AT` | Token expiration (ms) | Required |
| `BOT_USERNAME` | Bot trigger username | `techy` |
| `CLAUDE_MODEL` | Claude model to use | `claude-sonnet-4-20250514` |
| `MAX_DIFF_SIZE` | Max diff size in bytes | `100000` |
| `PORT` | Server port | `8080` |
| `LOG_LEVEL` | Logging level | `info` |

## Development

### Prerequisites

- Go 1.23+
- Docker (optional)

### Building

```bash
go build -o techy-bot ./cmd/techy
```

### Running Locally

```bash
# Load environment
export $(cat .env | xargs)

# Run
./techy-bot
```

### Project Structure

```
techy-bot/
├── cmd/techy/           # Entry point
├── internal/
│   ├── config/          # Configuration loading
│   ├── github/          # GitHub API client & webhooks
│   ├── oauth/           # OAuth token management
│   ├── claude/          # Claude API client
│   ├── review/          # Review logic & formatting
│   └── server/          # HTTP server
├── pkg/models/          # Shared types
├── Dockerfile
├── docker-compose.yml
└── README.md
```

## Production Deployment

For production, consider:

1. **TLS Termination**: Use a reverse proxy (nginx, Traefik, Caddy) with HTTPS
2. **Monitoring**: Add Prometheus metrics
3. **Logging**: Configure JSON logging and ship to your log aggregator
4. **Secrets Management**: Use Docker secrets or a vault solution

Example with Traefik:

```yaml
services:
  techy-bot:
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.techy.rule=Host(`techy.yourdomain.com`)"
      - "traefik.http.routers.techy.tls.certresolver=letsencrypt"
```

## Troubleshooting

### Token Refresh Fails

Check that your Claude subscription is active and the refresh token is valid.

### Webhook Not Received

1. Verify your webhook URL is publicly accessible
2. Check the webhook secret matches
3. Look at GitHub App webhook delivery logs

### Reviews Not Posted

1. Check the bot has write access to the repository
2. Verify the GitHub App is installed on the repository
3. Check server logs for errors

## Cost

TechyBot uses your existing Claude subscription:

| Solution | Cost |
|----------|------|
| Cursor BugBot | $40/user for 200 reviews |
| TechyBot (Claude Max) | Part of $100/month (unlimited*) |
| TechyBot (Claude Pro) | Part of $20/month |

*Subject to rate limits

## License

MIT
