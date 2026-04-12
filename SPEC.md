# vsync — Vault-Synced Shell Environment

## Overview

`vsync` is a CLI tool that creates a secure, vault-integrated shell environment. It:

1. **Stores** HashiCorp Vault credentials encrypted on disk using a locally-generated key.
2. **Launches** a new shell where configured commands are automatically shimmed so that required secrets are fetched from Vault and injected as environment variables at exec-time.
3. **Syncs** files from Vault to local paths on shell entry (or on demand).
4. **Caches** secrets encrypted on disk, respecting Vault lease/TTL expiry.

---

## Language & Tooling

| Concern | Choice | Rationale |
|---------|--------|-----------|
| Implementation language | **Go** | Single static binary, excellent `syscall.Exec`, rich crypto stdlib, official Vault Go SDK, fast CLI tooling with Cobra |
| CLI framework | `github.com/spf13/cobra` + `github.com/spf13/viper` | De-facto standard, good flag/env UX |
| Vault client | `github.com/hashicorp/vault/api` | Official SDK, handles token renewal, KV v1/v2 |
| Encryption | AES-256-GCM via `crypto/aes` + `crypto/rand` | Authenticated encryption, stdlib-only |
| Config parsing | `gopkg.in/yaml.v3` | Simple, well-supported |
| Dependency management | Go modules (`go.mod`) |  |
| Runtime tooling | `mise.toml` | Pins Go 1.26 and `slsa-verifier`; defines the build/test/install/verify tasks |

---

## Directory Layout

vsync keeps its long-lived state and its secret cache in separate locations.

The **state directory** is resolved in this order:
1. `VSYNC_STATE_DIR` if set (full override)
2. `XDG_STATE_HOME/vsync` if `XDG_STATE_HOME` is set
3. `~/.local/state/vsync` as the fallback

The **cache directory** is resolved independently:
1. `VSYNC_CACHE_DIR` if set (full override)
2. `XDG_CACHE_HOME/vsync` if `XDG_CACHE_HOME` is set
3. `~/.cache/vsync` as the fallback

All paths below refer to those resolved directories.

```
~/.config/vsync/
  config.yaml                  # user configuration (see below)

<state dir>/
  keys/
    default.key                # 32-byte random AES-256 key (0600)
  tokens/
    vault_addr.enc             # AES-GCM encrypted VAULT_ADDR
    vault_token.enc             # AES-GCM encrypted VAULT_TOKEN
  shims/
    <command-name>             # executable shim binary/script per configured command

<cache dir>/
  env/<key-name>.enc           # encrypted cached env-secret + expiry metadata
  files/<key-name>.enc         # encrypted cached file-secret + expiry metadata
```

---

## Configuration File

**Global path:** `$XDG_CONFIG_HOME/vsync/config.yaml` (or `~/.config/vsync/config.yaml` if `XDG_CONFIG_HOME` is unset).

`--config` / `VSYNC_CONFIG` point to a local override config. If they are not set,
`vsync` searches for `vsync.yaml` in the current directory and each parent
directory. Any override is merged on top of the global config from root-most to
leaf-most, so the closest local config wins for overlapping settings. Merge identity
is:
- commands: `env.commands[*].name`
- command variables: `env.commands[*].variables[*].name`
- files: `files[*].key`

```yaml
# Vault KV mount prefixes (optional — these are the defaults)
vault:
  env_prefix: "secret/data/vsync/env"      # prefix for env-var secrets
  files_prefix: "secret/data/vsync/files"  # prefix for file secrets
  # kv_version: 2  # 1 or 2 (default: 2)

# Per-command environment variable injection
env:
  commands:
    - name: pi                              # command name to shim
      variables:
        - name: GEMINI_API_KEY             # env var to set in the child process
          key: gemini-api-key              # vault key name (appended to env_prefix)
        - name: OPENAI_API_KEY
          key: openai-api-key

    - name: aws
      variables:
        - name: AWS_ACCESS_KEY_ID
          key: aws-access-key-id
        - name: AWS_SECRET_ACCESS_KEY
          key: aws-secret-access-key

# Files to sync from Vault on shell entry (and via `vsync sync`)
files:
  - path: ~/.pi/agent/auth.json            # local destination (~ is expanded)
    key: pi-agent-auth                     # vault key name (appended to files_prefix)
  - path: ~/.ssh/id_ed25519
    key: ssh-private-key
    mode: "0600"                           # optional file permission (default: 0600)
```

### Vault Secret Layout

Given the defaults above:

| Config entry | Full Vault path |
|---|---|
| `env.commands[*].variables[*].key: gemini-api-key` | `secret/data/vsync/env/gemini-api-key` |
| `files[*].key: pi-agent-auth` | `secret/data/vsync/files/pi-agent-auth` |

For **env secrets**, vsync reads the `value` field of the secret's `data` map:
```json
{ "data": { "value": "sk-abc123..." } }
```

For **file secrets**, vsync reads the `content` field:
```json
{ "data": { "content": "{ ... JSON ... }" } }
```

---

## CLI Interface

```
vsync [command] [flags]
```

### Global Flags

| Flag | Env Var | Description |
|------|---------|-------------|
| `--vault-addr` | `VAULT_ADDR` | Vault server address |
| `--vault-token` | `VAULT_TOKEN` | Vault token |
| `--global-config` | `VSYNC_GLOBAL_CONFIG` | Path to the global config file (default: `$XDG_CONFIG_HOME/vsync/config.yaml` or `~/.config/vsync/config.yaml`) |
| `--config` | `VSYNC_CONFIG` | Path to the local override config file (default: searched `vsync.yaml` in cwd/parents) |
| `--key` | `VSYNC_KEY` | Path to encryption key file (default: `<state dir>/keys/default.key`) |

Flags take precedence over environment variables; environment variables take precedence over stored/encrypted values.

---

### `vsync init`

**Purpose:** Store Vault credentials encrypted on disk and generate the local encryption key.

```
vsync init [--vault-addr ADDR] [--vault-token TOKEN] [--rotate-key]
```

**Steps:**

1. If `<state dir>/keys/default.key` does not exist (or `--rotate-key` is passed):
   - Generate 32 cryptographically random bytes via `crypto/rand`.
   - Write to key file with mode `0600`. Create parent dirs as needed.
2. Resolve `VAULT_ADDR` and `VAULT_TOKEN` (flag → env → prompt if missing).
3. Encrypt each value with AES-256-GCM using the key and write to:
   - `<state dir>/tokens/vault_addr.enc`
   - `<state dir>/tokens/vault_token.enc`
4. Verify connectivity: attempt a `vault.Auth().LookupSelf()` call.
5. Print confirmation; warn if the token has a short TTL.

---

### `vsync shell`

**Purpose:** Launch a new interactive shell with the vsync environment activated.

```
vsync shell [--shell /bin/zsh]
```

**Steps:**

1. Load and decrypt Vault credentials (or use flag/env overrides).
2. Read the global config file and any local override config (explicit `--config`/`VSYNC_CONFIG`, or searched `vsync.yaml` files in the current directory and parent directories), then merge them.
3. Sync all `files` entries (see **File Sync** below).
4. Build shims (see **Shim Mechanism** below) in `<state dir>/shims/`.
5. Construct child environment:
   - Copy current `os.Environ()`.
   - Prepend `<state dir>/shims` to `PATH`.
   - Set `VSYNC_ACTIVE=1` (prevents nested `vsync shell` calls).
   - Set `VSYNC_KEY` to the key file path so shims can locate it.
6. Detect shell from `--shell` flag → `$SHELL` → `/bin/sh`.
7. `syscall.Exec` into the shell (replace current process).

If `VSYNC_ACTIVE=1` is already set, print a warning and exit non-zero.

---

### `vsync exec <command> [args...]`

**Purpose:** Internal entry-point used by shims. Fetches secrets, sets env vars, then `exec`s the real command.

```
vsync exec <command> [args...]
```

**Steps:**

1. Load the merged config; find the matching `env.commands` entry for `<command>`.
2. For each `variable` in that entry:
   a. Check encrypted cache (`<cache dir>/env/<key>.enc`); use cached value if not expired.
   b. Otherwise decrypt Vault credentials, connect, fetch secret from `<env_prefix>/<key>`, cache result.
3. Build env: copy `os.Environ()`, add/override fetched variables.
4. Find the real binary: search `PATH` **skipping** `<state dir>/shims` to avoid re-entering the shim.
5. `syscall.Exec` into the real binary with the augmented environment.

---

### `vsync sync`

**Purpose:** Manually sync all `files` entries from Vault to local paths.

```
vsync sync [--file <key>]  # sync a specific file, or all if omitted
```

**Steps:**

1. Load the merged config and decrypt Vault credentials.
2. For each `files` entry (or the specified one):
   a. Check cache; if not expired, skip (unless `--force` flag is passed).
   b. Fetch secret from `<files_prefix>/<key>`; read `content` field.
   c. Expand `~` in `path`; create parent directories.
   d. Write content to `path` with configured `mode` (default `0600`).
   e. Update encrypted cache entry under `<cache dir>/files/<key>.enc` with TTL from Vault lease.
3. Print a summary of synced / skipped files.

---

### `vsync status`

**Purpose:** Show current configuration, stored credentials, and cache state.

```
vsync status
```

Output includes:
- Key file path and existence.
- Vault address (decrypted, displayed truncated).
- Token validity (calls Vault to check).
- List of configured commands and their shim presence.
- Cache entries with expiry times.

---

### `vsync cache clear`

```
vsync cache clear [--all | --env | --files | --key <name>]
```

Removes cached secret entries to force re-fetch on next use.

---

## Shim Mechanism

Shims are **small standalone executables** placed in `<state dir>/shims/`. They are generated once by `vsync shell` (recreated if config changes, detected via a hash of the relevant config section).

Each shim is a minimal Go-compiled binary (or a portable shell script as fallback) that simply calls:

```sh
#!/bin/sh
exec vsync exec "$(basename "$0")" "$@"
```

The shim directory is prepended to `PATH`, so calling `pi ...` resolves to the shim, which delegates to `vsync exec pi ...`, which fetches secrets and `exec`s the real `pi` binary found later in the unmodified `PATH`.

> **Alternative (shell-function approach):** If the user's shell is detected as bash/zsh, vsync may optionally source a shell snippet that defines functions instead of file-based shims — but file-based shims are the default for maximum compatibility.

---

## Encryption

### Key Generation

```
key = crypto/rand.Read(32 bytes)   // AES-256
```

### Encrypt

```
nonce = crypto/rand.Read(12 bytes)
ciphertext = AES-256-GCM.Seal(plaintext, nonce, additionalData="vsync/v1")
stored = nonce || ciphertext       // prepend nonce for self-contained blob
```

### Decrypt

```
nonce = stored[:12]
ciphertext = stored[12:]
plaintext = AES-256-GCM.Open(ciphertext, nonce, additionalData="vsync/v1")
```

All `.enc` files contain the raw binary blob (nonce + ciphertext). Files are written atomically (write to temp, then rename) with mode `0600`.

---

## Cache Format

Each cache file at `<cache dir>/{env,files}/<key>.enc` stores an encrypted JSON blob:

```json
{
  "value": "<secret value>",
  "expires_at": "2026-04-11T12:00:00Z",
  "vault_path": "secret/data/vsync/env/gemini-api-key"
}
```

- `expires_at` is derived from the Vault lease duration (`lease_duration` seconds from fetch time), minus a 10-second safety margin.
- If Vault returns `lease_duration: 0` (non-renewable / infinite), the entry is treated as **non-expiring** and cached indefinitely until manually cleared.

---

## Error Handling & UX

- All errors are printed to `stderr` with a short contextual prefix (`vsync: `).
- Exit codes: `0` success, `1` general error, `2` misconfiguration, `3` vault unreachable.
- When Vault is unreachable and a valid (non-expired) cache entry exists, vsync **falls back to cache** and prints a warning to `stderr`.
- `vsync shell` never silently continues if `vsync init` has not been run — it prints a clear message: `vsync: vault credentials not found; run 'vsync init' first`.

---

## Security Considerations

- Key file and all `.enc` files are created with mode `0600`; directories with `0700`.
- The encryption key never leaves disk except in memory during a vsync process.
- `VSYNC_KEY` env var in the shimmed shell points to the key file path; the key itself is never stored in environment variables.
- Vault token is decrypted on demand and not stored in memory longer than necessary.
- Cache files use the same AES-256-GCM scheme as token files.
- `vsync exec` avoids logging or printing secret values; `--verbose` mode redacts secret values in output.

---

## `mise.toml`

```toml
[tools]
"github:slsa-framework/slsa-verifier" = "2.7.1"
go = "1.26"

[tasks.build]
run = "go build -o dist/vsync ./cmd/vsync"

[tasks.test]
run = "go test ./..."

[tasks.install]
run = "go install ./cmd/vsync"

# `verify` downloads a published release and checks its SLSA provenance.
```

---

## Project Structure

```
vsync/
  cmd/
    vsync/
      main.go                  # entry point; registers cobra commands
  internal/
    config/
      config.go                # load & merge config files (global config + optional local override)
    crypto/
      crypto.go                # key generation, AES-GCM encrypt/decrypt
    vault/
      client.go                # vault connection, KV get (v1/v2)
      cache.go                 # read/write encrypted cache entries
    shim/
      shim.go                  # generate & manage shim scripts
    shell/
      shell.go                 # build env, exec into shell
    state/
      state.go                 # manage resolved vsync state paths
  go.mod
  go.sum
  mise.toml
  SPEC.md
```

---

## Implementation Plan

1. **Scaffold** — `mise.toml`, `go.mod`, cobra root command, global flags. Run `mise install`.
2. **Crypto layer** — `internal/crypto`: key gen, encrypt, decrypt. Unit tests.
3. **State layer** — `internal/state`: path helpers, atomic file write, dir creation.
4. **`vsync init`** — store encrypted credentials.
5. **Vault client** — `internal/vault`: connect, KV v2 get, cache integration.
6. **`vsync sync`** — file sync command.
7. **Shim generation** — `internal/shim`: write shim scripts, track config hash.
8. **`vsync exec`** — secret fetch + `syscall.Exec`.
9. **`vsync shell`** — sync + shim setup + exec shell.
10. **`vsync status` / `vsync cache clear`** — observability commands.
11. **Integration tests** — use `devenv.nix`, which runs Vault in dev mode with a fixed root token for deterministic tests.
