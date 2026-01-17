# TechyBot Quick Start Guide

A self-hosted GitHub code review bot using Claude Code CLI that posts **inline comments with line numbers** directly on your PRs!

## Features

✅ **Inline Comments**: Claude posts comments on specific lines with file paths and line numbers
✅ **Multiple Review Modes**: Hunt bugs, analyze security, check performance, or comprehensive reviews
✅ **Uses Claude Code CLI**: Leverages your existing Claude subscription (Free/Pro/Max)
✅ **Self-Hosted**: Full control over your data and deployment

## Setup (5 minutes)

### 1. Create a GitHub App

1. Go to https://github.com/settings/apps/new
2. Fill in:
   - **Name**: TechyBot (or any name)
   - **Homepage URL**: `http://your-server.com`
   - **Webhook URL**: `http://your-server.com/webhook`
   - **Webhook secret**: Generate a random secret (save it!)

3. **Permissions**:
   - Contents: **Read**
   - Issues: **Read & Write**
   - Pull requests: **Read & Write**
   - Metadata: **Read**

4. **Subscribe to events**:
   - Issue comment
   - Pull request
   - Pull request review comment

5. **Generate and download private key** → Save as `github-private-key.pem`
6. Note your **App ID**

### 2. Configure TechyBot

Edit `.env`:

\`\`\`bash
# GitHub App (from step 1)
GITHUB_APP_ID=123456  # Your App ID
GITHUB_WEBHOOK_SECRET=your-secret-here
GITHUB_PRIVATE_KEY_PATH=./github-private-key.pem

# Claude Code CLI (already installed on your system)
CLAUDE_PATH=claude
CLAUDE_MODEL=sonnet

# Bot settings
BOT_USERNAME=techy
PORT=8080
\`\`\`

### 3. Ensure Claude Code is Authenticated

\`\`\`bash
# Verify Claude Code is installed and authenticated
claude --version

# If not authenticated, run:
claude

# This will prompt you to log in with your Claude account
\`\`\`

### 4. Run TechyBot

\`\`\`bash
# Run directly
./techy-bot

# Or with Docker
docker-compose up --build
\`\`\`

### 5. Install GitHub App

1. Go to your GitHub App settings
2. Click "Install App"
3. Select repositories where you want TechyBot

### 6. Make Your Server Public (for webhooks)

For local testing, use ngrok:

\`\`\`bash
# Install ngrok: https://ngrok.com/
ngrok http 8080

# Update your GitHub App webhook URL to the ngrok URL:
# https://abc123.ngrok.io/webhook
\`\`\`

For production, deploy to a server with a public IP or domain.

## Usage

### In Pull Requests

Comment on any PR with:

- `@techy hunt` - Quick bug detection (like Cursor's BugBot) 🐛
- `@techy security` - Security audit 🔒
- `@techy performance` - Performance analysis ⚡
- `@techy analyze` - Deep technical analysis 🔬
- `@techy review` - Comprehensive code review 📝

### Example

1. Create a PR
2. Comment: `@techy hunt`
3. TechyBot will:
   - React with 👀 (processing)
   - Review your code with Claude Code CLI
   - Post inline comments with specific line numbers
   - React with 🚀 (done)

## How It Works

\`\`\`
┌─────────────────┐
│   GitHub PR     │
│  @techy hunt    │
└────────┬────────┘
         │
         │ Webhook
         ▼
┌─────────────────┐
│  TechyBot       │
│  (Go Server)    │
└────────┬────────┘
         │
         │ Executes
         ▼
┌─────────────────┐      ┌──────────────┐
│  Claude Code    │──────│ PR Changes   │
│  CLI Review     │      │ (git diff)   │
└────────┬────────┘      └──────────────┘
         │
         │ Structured Output
         │ FILE: file.go:123
         │ COMMENT: Bug found...
         ▼
┌─────────────────┐
│  GitHub API     │
│  Posts inline   │
│  comments       │
└─────────────────┘
\`\`\`

## Inline Comments Format

Claude is instructed to output in this format:

\`\`\`
FILE: internal/server/server.go:45
COMMENT: 🐛 **Bug**: Missing error handling for nil pointer
\`\`\`

TechyBot parses this and posts it as an inline comment on line 45 of `internal/server/server.go`!

## Troubleshooting

### Bot doesn't respond
1. Check TechyBot logs: `docker-compose logs -f techy-bot`
2. Verify GitHub App is installed on the repository
3. Check webhook deliveries in GitHub App settings
4. Ensure webhook URL is publicly accessible

### Claude Code errors
1. Verify: `claude --version` works
2. Verify: `which claude` shows the correct path
3. Check authentication: `claude` (should not ask to log in)
4. Check logs: `LOG_LEVEL=debug ./techy-bot`

### Inline comments not posting
1. Check that PR has changes (git diff not empty)
2. Verify the file paths in Claude's output match actual PR files
3. Check GitHub App has "Pull requests: Read & Write" permission
4. Look for fallback: Bot posts as regular comment if inline fails

## Configuration Options

| Variable | Description | Default |
|----------|-------------|---------|
| `GITHUB_APP_ID` | Your GitHub App ID | Required |
| `GITHUB_WEBHOOK_SECRET` | Webhook secret | Required |
| `GITHUB_PRIVATE_KEY_PATH` | Path to private key | `./github-private-key.pem` |
| `CLAUDE_PATH` | Path to Claude Code CLI | `claude` |
| `CLAUDE_MODEL` | Model (sonnet/opus/haiku) | `sonnet` |
| `BOT_USERNAME` | Trigger username | `techy` |
| `MAX_DIFF_SIZE` | Max diff size in bytes | `100000` |
| `PORT` | Server port | `8080` |
| `LOG_LEVEL` | Log level | `info` |

## Next Steps

- Deploy to a VPS or cloud provider (AWS, DigitalOcean, etc.)
- Set up HTTPS with a reverse proxy (nginx, Caddy)
- Configure multiple repositories
- Customize review prompts in `internal/claude/prompts.go`

## Architecture

- **Language**: Go 1.23+
- **Claude Integration**: Claude Code CLI (via subprocess)
- **GitHub Integration**: go-github SDK
- **Webhook Server**: Gorilla Mux
- **Deployment**: Docker + docker-compose

## License

MIT
