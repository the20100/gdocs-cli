# gdocs — Google Docs CLI

A command-line tool for the [Google Docs API](https://developers.google.com/workspace/docs/api/reference/rest). Create documents, read content, insert text, find & replace, and delete ranges — all from your terminal or scripts.

## Install

```bash
git clone https://github.com/the20100/gdocs-cli
cd gdocs-cli
go build -o gdocs .
mv gdocs /usr/local/bin/
```

## Authentication

`gdocs` uses OAuth 2.0 Bearer tokens. Three ways to authenticate:

### Option 1 — gcloud CLI (quickest, token expires in ~1h)

```bash
gdocs auth set-token $(gcloud auth print-access-token)
```

### Option 2 — Browser OAuth flow (persistent)

Requires a Google Cloud project with the Docs API enabled and OAuth 2.0 credentials (Desktop app type).

```bash
export GDOCS_CLIENT_ID=your_client_id
export GDOCS_CLIENT_SECRET=your_client_secret
gdocs auth login
```

### Option 3 — Environment variable (no config file)

```bash
export GDOCS_ACCESS_TOKEN=$(gcloud auth print-access-token)
```

Token resolution order: `GDOCS_ACCESS_TOKEN` env var → stored config file.

## Commands

### auth

```bash
gdocs auth set-token <token>          # Save a token to config
gdocs auth login                      # Browser OAuth flow (persistent)
gdocs auth login --no-browser         # Manual flow for remote/VPS
gdocs auth status                     # Show current auth status
gdocs auth logout                     # Remove saved token
```

With `--no-browser`: the CLI prints the OAuth URL. Open it in a local browser, authorize, then copy the full redirect URL from the address bar and paste it into the terminal (the page will fail to load — that's expected).

### doc

```bash
# Create a new document
gdocs doc create "My Document"

# Get document metadata (id, title, revision)
gdocs doc get <document-id>

# Read plain text content
gdocs doc content <document-id>

# Insert text at end of document
gdocs doc insert <document-id> "Hello, world!"

# Insert text at a specific index
gdocs doc insert <document-id> "Prefix: " --index 1

# Find and replace text
gdocs doc replace <document-id> --find "old text" --replace "new text"
gdocs doc replace <document-id> --find "TODO" --replace "DONE" --case-sensitive

# Delete a range of content
gdocs doc delete-range <document-id> --start 1 --end 10
```

### Global flags

| Flag | Description |
|------|-------------|
| `--json` | Force JSON output |
| `--pretty` | Force pretty-printed JSON (implies --json) |

Output is **auto-detected**: JSON when piped, human-readable when in a terminal.

```bash
# Pipe to jq
gdocs doc get <id> --json | jq '.title'

# Read content and search
gdocs doc content <id> | grep "keyword"
```

### Other

```bash
gdocs info     # Show binary location, config path, env vars
gdocs update   # Update to latest version from GitHub
```

## Setting up Google Cloud OAuth credentials

1. Go to [Google Cloud Console](https://console.cloud.google.com/)
2. Create or select a project
3. Enable the [Google Docs API](https://console.cloud.google.com/apis/library/docs.googleapis.com)
4. Go to **APIs & Services > Credentials > Create Credentials > OAuth client ID**
5. Choose **Desktop app** as the application type
6. Copy the Client ID and Client Secret
7. Export them and run login:
   ```bash
   export GDOCS_CLIENT_ID=your_client_id
   export GDOCS_CLIENT_SECRET=your_client_secret
   gdocs auth login
   ```

## Document IDs

The document ID is the long string in a Google Docs URL:

```
https://docs.google.com/document/d/1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgVE2upms/edit
                                   ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
                                   this is the document ID
```
