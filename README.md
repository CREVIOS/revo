# TechyBot

A self-hosted GitHub code review bot powered by Claude Code CLI. TechyBot provides intelligent code reviews directly in your pull requests, similar to Cursor's BugBot.

## Features

- **Multiple Review Modes**: Choose from different review styles based on your needs
  - `@techy review` - Standard comprehensive code review
  - `@techy hunt` - Quick bug detection (like BugBot)
  - `@techy security` - Security-focused analysis
  - `@techy performance` - Performance optimization suggestions
  - `@techy analyze` - Deep technical analysis

- **Uses Claude Code CLI**: Leverages your existing Claude Code installation and authentication
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

### 2. Install and Authenticate Claude Code CLI

TechyBot uses the Claude Code CLI for reviews. You need to have it installed and authenticated:

1. **Install Claude Code CLI** (if not already installed):
   ```bash
   npm install -g @anthropic-ai/claude-code
   ```

2. **Authenticate with Claude**:
   ```bash
   claude
   ```
   This will prompt you to authenticate with your Claude account (Free, Pro, or Max subscription).

### 3. Configure Environment

```bash
cp .env.example .env
```

Edit `.env` with your values:

```bash
# GitHub App
GITHUB_APP_ID=123456
GITHUB_WEBHOOK_SECRET=your-webhook-secret

# Claude Code CLI (uses 'claude' from PATH by default)
CLAUDE_PATH=claude
CLAUDE_MODEL=sonnet  # or opus, haiku

# Optional: Anthropic API key (if set, CLI will use API-key auth)
ANTHROPIC_API_KEY=

# Optional: long-lived OAuth token created by `claude setup-token`
CLAUDE_CODE_OAUTH_TOKEN=

# Optional Claude OAuth (used by internal/oauth if needed)
CLAUDE_ACCESS_TOKEN=
CLAUDE_REFRESH_TOKEN=
CLAUDE_EXPIRES_AT=
CLAUDE_CREDENTIALS_FILE=

# Bot settings
BOT_USERNAME=techy

# Database
DATABASE_URL=postgres://techy:techy_pass@localhost:5432/techybot?sslmode=disable

# Redis / Asynq
REDIS_ADDR=localhost:6379
REDIS_PASSWORD=
REDIS_DB=0
ASYNQ_QUEUE=reviews
ASYNQ_CONCURRENCY=3
ASYNQ_MAX_RETRY=10

# Admin API (required for /api/* endpoints)
ADMIN_API_KEY=change-me
```

### Using Claude Code OAuth (Subscription)

1) Create a long-lived token with the CLI:
```
claude setup-token
```
Copy the token shown (valid for 1 year).

2) Put it in your `.env`:
```
CLAUDE_CODE_OAUTH_TOKEN=...
```

3) Make sure `ANTHROPIC_API_KEY` is **not** set, otherwise the CLI will use the API key instead.

4) Start the services:
```
docker-compose up --build
```
Or locally:
```
./techy-bot
./techy-bot worker
```

Copy your GitHub App private key:

```bash
cp /path/to/your-app.private-key.pem ./github-private-key.pem
```

**Important**: The bot will use your Claude Code CLI authentication from `~/.claude/`. Make sure you've authenticated before running the bot.

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
| `CLAUDE_PATH` | Path to Claude Code CLI | `claude` |
| `CLAUDE_MODEL` | Claude model to use | `sonnet` |
| `ANTHROPIC_API_KEY` | Anthropic API key (optional) | `` |
| `CLAUDE_CODE_OAUTH_TOKEN` | Long-lived Claude Code OAuth token (optional) | `` |
| `CLAUDE_ACCESS_TOKEN` | Claude OAuth access token (optional) | `` |
| `CLAUDE_REFRESH_TOKEN` | Claude OAuth refresh token (optional) | `` |
| `CLAUDE_EXPIRES_AT` | Claude OAuth expiry (ms epoch) | `` |
| `CLAUDE_CREDENTIALS_FILE` | OAuth credentials file path | `` |
| `BOT_USERNAME` | Bot trigger username | `techy` |
| `MAX_DIFF_SIZE` | Max diff size in bytes | `100000` |
| `PORT` | Server port | `8080` |
| `LOG_LEVEL` | Logging level | `info` |
| `DATABASE_URL` | Postgres connection string | Required |
| `ADMIN_API_KEY` | Admin API key for /api endpoints | Required |
| `REDIS_ADDR` | Redis address for Asynq | `localhost:6379` |
| `REDIS_PASSWORD` | Redis password | Optional |
| `REDIS_DB` | Redis DB number | `0` |
| `ASYNQ_QUEUE` | Asynq queue name | `reviews` |
| `ASYNQ_CONCURRENCY` | Worker concurrency | `3` |
| `ASYNQ_MAX_RETRY` | Max task retries | `10` |

## Development

### Prerequisites

- Go 1.23+
- Node.js 18+ and npm (for Claude Code CLI)
- Claude Code CLI authenticated with your Claude account
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

Run the background worker in a separate terminal:

```bash
./techy-bot worker
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

TechyBot uses your existing Claude Code subscription:

| Solution | Cost |
|----------|------|
| Cursor BugBot | $40/user for 200 reviews |
| TechyBot (Claude Max) | Part of $100/month (unlimited*) |
| TechyBot (Claude Pro) | Part of $20/month (subject to limits) |
| TechyBot (Claude Free) | Free tier usage limits |

*Subject to Claude's usage limits and rate limiting

## Admin API

TechyBot exposes a protected admin API for metrics and CRUD access to stored data.

**Auth:** Send `X-Admin-API-Key: <key>` or `Authorization: Bearer <key>`.

**Endpoints:**
- `GET /api/metrics`
- `/api/reviews`
- `/api/review-comments`
- `/api/repositories`
- `/api/webhook-events`
- `/api/worker-metrics`
- `/api/api-keys`

## MCP (Optional)

If you want Claude Code to use additional tools via MCP, drop a minimal `.mcp.json` in the repo root. This is optional (you already get GitHub access via your GitHub OAuth / webhook flow).

Example `.mcp.json` (minimal, safe stack):

```json
{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/your/repo"]
    },
    "git": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-git", "/path/to/your/repo"]
    },
    "fetch": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-fetch"]
    },
    "memory": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-memory"]
    },
    "time": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-time"]
    },
    "sequential-thinking": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-sequential-thinking"]
    }
  }
}
```

Notes:
- Replace `/path/to/your/repo` with the absolute path to this repo.
- Keep the MCP list small to reduce risk surface.

## License

MIT
