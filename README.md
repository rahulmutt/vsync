# vsync

`vsync` gives you a secure, Vault-integrated shell environment.
Run `vsync shell` once and every command you've configured automatically receives its
secrets from HashiCorp Vault — no `.env` files, no copy-pasted tokens, no secrets in your
shell history.

```
$ vsync shell
vsync: synced pi-agent-auth → ~/.pi/agent/auth.json
vsync: launching /bin/zsh with 2 shim(s)

$ pi chat "hello"          # GEMINI_API_KEY injected transparently
$ aws s3 ls                # AWS_ACCESS_KEY_ID + AWS_SECRET_ACCESS_KEY injected transparently
```

---

## How it works

1. **`vsync init`** stores your Vault address and token encrypted on disk using a
   randomly-generated AES-256 key that never leaves your machine.
2. **`vsync shell`** opens a new shell where a shim directory is prepended to your
   `PATH`. Each shim is a tiny wrapper script for a command you've configured.
3. When you run a shimmed command (e.g. `pi`), the shim calls `vsync exec pi`, which
   fetches the required secrets from Vault, injects them as environment variables, and
   immediately `exec`s the real binary — the secrets exist only in the child process's
   environment and are never written to disk in plain text.
4. Secrets are **cached encrypted on disk**, so subsequent invocations are instant and
   the tool keeps working even when Vault is temporarily unreachable.

---

## Requirements

- A running [HashiCorp Vault](https://developer.hashicorp.com/vault) instance
- A Vault token with read access to the relevant KV paths
- [mise](https://mise.jdx.dev/) (to build from source) — or just copy a pre-built binary

---

## Installation

### Build from source

```sh
git clone https://github.com/vsync/vsync
cd vsync
mise install     # pins and installs Go 1.25
mise run build   # produces dist/vsync
cp dist/vsync /usr/local/bin/vsync
```

### Verify

```sh
vsync --help
```

---

## Quick start

### 1. Write a config file

```sh
mkdir -p ~/.config/vsync
```

`~/.config/vsync/config.yaml`:

```yaml
env:
  commands:
    - name: pi
      variables:
        - name: GEMINI_API_KEY
          key: gemini-api-key

    - name: aws
      variables:
        - name: AWS_ACCESS_KEY_ID
          key: aws-access-key-id
        - name: AWS_SECRET_ACCESS_KEY
          key: aws-secret-access-key

files:
  - path: ~/.pi/agent/auth.json
    key: pi-agent-auth
```

### 2. Store the matching secrets in Vault

```sh
# Env secrets — stored at secret/data/vsync/env/<key>
vault kv put secret/vsync/env/gemini-api-key    value="sk-..."
vault kv put secret/vsync/env/aws-access-key-id value="AKIA..."
vault kv put secret/vsync/env/aws-secret-access-key value="..."

# File secrets — stored at secret/data/vsync/files/<key>
vault kv put secret/vsync/files/pi-agent-auth content='{"token":"..."}'
```

> See [Vault secret layout](#vault-secret-layout) for the exact path format and field names.

### 3. Initialise vsync

```sh
vsync init
# Vault address (VAULT_ADDR): https://vault.example.com
# Vault token (VAULT_TOKEN): ••••••••
# vsync: verifying vault connectivity… ✓
# vsync: credentials stored at <state dir>/tokens
# vsync: encryption key at     <state dir>/keys/default.key
```

You can also pass credentials as flags or environment variables to skip the prompts:

```sh
VAULT_ADDR=https://vault.example.com VAULT_TOKEN=hvs.xxx vsync init
vsync init --vault-addr https://vault.example.com --vault-token hvs.xxx
```

If you want vsync to store its state somewhere other than the default, set
`VSYNC_STATE_DIR` for a full override or `XDG_STATE_DIR` to use `$XDG_STATE_DIR/vsync`
before running `vsync init`.

### 4. Enter the vsync shell

```sh
vsync shell
```

That's it. From this shell, every configured command automatically gets its secrets.

---

## Configuration reference

**Location:** `~/.config/vsync/config.yaml`  
Override with `--config <path>` or `VSYNC_CONFIG=<path>`.

```yaml
# Optional vault settings (these are the defaults)
vault:
  env_prefix:   "secret/data/vsync/env"    # KV path prefix for env secrets
  files_prefix: "secret/data/vsync/files"  # KV path prefix for file secrets
  kv_version:   2                           # 1 or 2

# Commands to shim and the env vars each one needs
env:
  commands:
    - name: pi                    # exact binary name to intercept
      variables:
        - name: GEMINI_API_KEY    # environment variable set in the child process
          key: gemini-api-key     # vault key appended to env_prefix

    - name: aws
      variables:
        - name: AWS_ACCESS_KEY_ID
          key: aws-access-key-id
        - name: AWS_SECRET_ACCESS_KEY
          key: aws-secret-access-key

# Files synced from Vault on shell entry and via `vsync sync`
files:
  - path: ~/.pi/agent/auth.json   # destination on disk (~ is expanded)
    key: pi-agent-auth            # vault key appended to files_prefix
    mode: "0600"                  # optional permission (default: 0600)
```

### Vault secret layout

| Config entry | Vault path | Expected field |
|---|---|---|
| `env.commands[*].variables[*].key: gemini-api-key` | `secret/data/vsync/env/gemini-api-key` | `value` |
| `files[*].key: pi-agent-auth` | `secret/data/vsync/files/pi-agent-auth` | `content` |

**Env secrets** must have a `value` field:
```sh
vault kv put secret/vsync/env/gemini-api-key value="sk-abc123"
```

**File secrets** must have a `content` field:
```sh
vault kv put secret/vsync/files/pi-agent-auth content='{"auth":"..."}'
```

---

## Commands

### `vsync init`

Store Vault credentials encrypted on disk. Run once, or again after rotating your token.

```
vsync init [--vault-addr ADDR] [--vault-token TOKEN] [--rotate-key]
```

| Flag | Description |
|------|-------------|
| `--vault-addr` | Vault server address (overrides `VAULT_ADDR`) |
| `--vault-token` | Vault token (overrides `VAULT_TOKEN`) |
| `--rotate-key` | Generate a fresh encryption key and re-encrypt stored credentials |

`init` will prompt interactively for any values not provided via flag or environment
variable. The token prompt does not echo characters when run in a real terminal.

After storing credentials it immediately verifies connectivity and warns you if the token
has less than an hour remaining.

---

### `vsync shell`

Launch a new interactive shell with the vsync environment active.

```
vsync shell [--shell /bin/zsh]
```

What happens when you run it:

1. Configured files are synced from Vault to their local paths.
2. Shim scripts are written (or refreshed) for every configured command.
3. A new shell is started with:
   - The shim directory prepended to `PATH`
   - `VSYNC_ACTIVE=1` (prevents accidental nesting)
   - `VSYNC_KEY` set to the encryption key file path

Running `vsync shell` inside an existing vsync shell is blocked — you'll see an error
instead of ending up with a broken nested environment.

---

### `vsync sync`

Manually pull file secrets from Vault to their local paths.

```
vsync sync [--file <key>] [--force]
```

| Flag | Description |
|------|-------------|
| `--file <key>` | Sync only the entry whose vault key matches `<key>` |
| `--force` | Re-fetch even if the cache is still fresh |

```sh
vsync sync                      # sync all files
vsync sync --file pi-agent-auth # sync one file
vsync sync --force              # bypass cache, re-fetch everything
```

---

### `vsync status`

Show the current state of vsync: key file, vault connectivity, token TTL, shim presence,
and cache expiry for every configured secret.

```
vsync status
```

Example output:

```
=== vsync status ===
  Key file:            <state dir>/keys/default.key
  Vault address:       https://vault.example.com
  Token TTL:           11h59m42s
  Config file:         ~/.config/vsync/config.yaml

Configured commands (2):
  pi                    (shim present)
    GEMINI_API_KEY = gemini-api-key [cached (expires in 11h59m32s)]
  aws                   (shim present)
    AWS_ACCESS_KEY_ID = aws-access-key-id [not cached]
    AWS_SECRET_ACCESS_KEY = aws-secret-access-key [not cached]

File sync entries (1):
  pi-agent-auth   → ~/.pi/agent/auth.json   [exists, cached]
```

---

### `vsync cache clear`

Remove cached secrets to force a fresh fetch from Vault on the next use.

```
vsync cache clear [--all] [--env] [--files] [--key <name>]
```

| Flag | What gets cleared |
|------|-------------------|
| `--all` | Every cached secret (env + files) |
| `--env` | All cached env-variable secrets |
| `--files` | All cached file secrets |
| `--key <name>` | One specific entry by vault key name |

```sh
vsync cache clear --all
vsync cache clear --env
vsync cache clear --key gemini-api-key
vsync cache clear --files --key pi-agent-auth
```

---

## Global flags

These flags work with every command.

| Flag | Environment variable | Description |
|------|---------------------|-------------|
| `--vault-addr` | `VAULT_ADDR` | Vault server address |
| `--vault-token` | `VAULT_TOKEN` | Vault token |
| `--config` | `VSYNC_CONFIG` | Config file path |
| `--key` | `VSYNC_KEY` | Encryption key file path (defaults to `<state dir>/keys/default.key`) |

Flags take precedence over environment variables, which take precedence over the
encrypted values stored by `vsync init`.

---

## Where things are stored

The vsync state directory is resolved in this order:
1. `VSYNC_STATE_DIR` if set (full override)
2. `XDG_STATE_DIR/vsync` if `XDG_STATE_DIR` is set
3. `~/.local/state/vsync` as the fallback

```
~/.config/vsync/
  config.yaml                          ← your configuration

<state dir>/
  keys/
    default.key                        ← 32-byte AES-256 key (never leaves disk)
  tokens/
    vault_addr.enc                     ← encrypted Vault address
    vault_token.enc                    ← encrypted Vault token
  cache/
    env/<key>.enc                      ← cached env secrets with expiry
    files/<key>.enc                    ← cached file secrets with expiry
  shims/
    <command>                          ← one tiny shell script per configured command
```

All `.enc` files are AES-256-GCM encrypted binary blobs (nonce + ciphertext). All files
are created with permissions `0600`; directories with `0700`.

---

## Caching behaviour

vsync caches secrets encrypted on disk so that:

- **Repeated calls are fast.** Running `pi` fifty times in a session means one Vault
  round-trip, not fifty.
- **Offline work keeps working.** If Vault is unreachable but a cached value exists (even
  if expired), vsync uses the stale cache and prints a warning to stderr rather than
  blocking your work.

Cache lifetime comes from the Vault lease duration of each secret minus a 10-second
safety margin. Secrets with `lease_duration: 0` (e.g. root tokens, static secrets with
no TTL) are cached indefinitely until you clear them manually.

Use `vsync status` to see expiry times and `vsync cache clear` to invalidate entries.

---

## Security notes

- **The key never leaves disk.** The 32-byte AES key lives only in the resolved state
  directory (`<state dir>/keys/default.key`) and in process memory while vsync is running.
  It is never exported as an environment variable.
- **Credentials are decrypted on demand.** The Vault token is decrypted when needed and
  not retained in memory beyond the operation that required it.
- **Secrets are never logged.** `vsync exec` does not print secret values to stdout or
  stderr under any circumstances.
- **Shims are transparent.** The shimmed shell environment is otherwise identical to your
  normal shell — only the targeted binaries receive injected secrets.
- **File permissions are enforced.** All secrets files are `0600`; directories are `0700`.
  Writes go through a temp-file-then-rename sequence to be atomic.

---

## Troubleshooting

### `vsync: vault credentials not found; run 'vsync init' first`

You haven't run `vsync init` yet, or you're pointing at a different state directory.
Run `vsync init` and follow the prompts.

### Vault is unreachable but I need to work now

vsync will automatically fall back to cached values and print a warning:

```
vsync: vault unavailable, using stale cache for gemini-api-key
```

If there's no cache entry yet, you'll need Vault connectivity for the first fetch.

### My token expired and now nothing works

Run `vsync init` again with your new token:

```sh
vsync init --vault-token hvs.newtoken
```

Your existing encryption key is reused; only the stored token is updated.

### I need to rotate the encryption key

```sh
vsync init --rotate-key
```

This generates a new 32-byte key and re-encrypts the stored Vault credentials under it.
Cached secrets will be unreadable after rotation; they'll be re-fetched automatically on
next use.

### A shimmed command isn't picking up the right binary

Make sure the real binary is somewhere later in `PATH` than the shim directory
(`<state dir>/shims` after resolution). Check with:

```sh
which -a pi     # should show the shim first, real binary second
```

### I accidentally ran `vsync shell` inside a vsync shell

vsync detects the `VSYNC_ACTIVE=1` environment variable and refuses to nest:

```
vsync: already inside a vsync shell; nested shells are not supported
```

Exit the current shell first, then re-enter with `vsync shell`.

### Check overall health

```sh
vsync status
```

This shows key file existence, Vault connectivity, token TTL, shim presence, and cache
state in one view.
