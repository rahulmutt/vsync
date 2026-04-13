# vsync — Vault-synced shell environment

## Overview

`vsync` is a Go CLI that:

1. stores Vault connection credentials encrypted on disk,
2. launches a shell with command shims that inject Vault secrets at exec-time,
3. syncs Vault-backed files to local paths on shell entry or on demand, and
4. caches fetched secrets encrypted on disk with lease-aware expiry.

The canonical secret sources are Vault KV paths. Each configured secret reference can choose a **vault profile**, so one command can draw from multiple Vault servers / credentials.

---

## Config file

**Global path:** `$XDG_CONFIG_HOME/vsync/config.yaml` or `~/.config/vsync/config.yaml`.

`--config` / `VSYNC_CONFIG` select a local override file. If omitted, `vsync` searches for `vsync.yaml` in the current directory and each parent directory. Global and local configs are merged top-to-bottom; nearer local config wins for overlapping entries.

Merge identity:
- commands: `env.commands[*].name`
- command variables: `env.commands[*].variables[*].name`
- files: `files[*].key`

### Configuration model

```yaml
vault:
  # default profile (used when a secret reference omits profile)
  env_prefix: "secret/data/vsync/env"
  files_prefix: "secret/data/vsync/files"
  kv_version: 2

  # optional bootstrap values used by `vsync init` or as fallbacks
  addr: "http://127.0.0.1:8200"
  token: "hvs.root-token"

  # additional profiles
  profiles:
    prod:
      addr: "https://vault.prod.example.com"
      token: "hvs.prod-token"
      env_prefix: "secret/data/prod/env"
      files_prefix: "secret/data/prod/files"
      kv_version: 2
    staging:
      addr: "https://vault.staging.example.com"
      token: "hvs.staging-token"
      env_prefix: "secret/data/staging/env"
      files_prefix: "secret/data/staging/files"
      kv_version: 1

env:
  commands:
    - name: pi
      filter: 'args.exists(a, a == "--with-secrets")'
      variables:
        - name: GEMINI_API_KEY
          key: gemini-api-key
          profile: prod
        - name: OPENAI_API_KEY
          key: openai-api-key
          profile: staging
        - name: ANTHROPIC_API_KEY
          key: anthropic-api-key   # defaults to the default profile

files:
  - path: ~/.pi/agent/auth.json
    key: pi-agent-auth
    profile: prod
  - path: ~/.ssh/id_ed25519
    key: ssh-private-key
    profile: staging
    mode: "0600"
```

### Profile rules

- `vault:` at the top level is the **default profile**.
- `vault.profiles.<name>` defines additional profiles.
- Each secret reference may include `profile: <name>`.
- If `profile` is omitted, the default profile is used.
- `env_prefix`, `files_prefix`, and `kv_version` default per profile if omitted.

### Command filters

Each `env.commands` entry may set `filter:` to a CEL expression evaluated against the command arguments as `args` (a `list<string>`).
If the expression returns `true`, secrets are injected as usual.
If it returns `false`, the command runs without any Vault-derived environment variables.

### Vault secret layout

For a reference such as `profile: prod` and `key: gemini-api-key`, vsync reads:

- env secret path: `<prod env_prefix>/gemini-api-key`
- file secret path: `<prod files_prefix>/pi-agent-auth`

Env secrets are expected to contain:

```json
{ "data": { "value": "sk-..." } }
```

File secrets are expected to contain:

```json
{ "data": { "content": "..." } }
```

---

## Storage layout

vsync keeps long-lived state and cache separate.

Resolved state directory:
1. `VSYNC_STATE_DIR`
2. `XDG_STATE_HOME/vsync`
3. `~/.local/state/vsync`

Resolved cache directory:
1. `VSYNC_CACHE_DIR`
2. `XDG_CACHE_HOME/vsync`
3. `~/.cache/vsync`

### State directory

```text
<state dir>/
  keys/
    default.key
  tokens/
    vault_addr.enc                 # default profile
    vault_token.enc                # default profile
    <profile>/vault_addr.enc       # additional profiles
    <profile>/vault_token.enc
  shims/
    <command-name>
```

### Cache directory

```text
<cache dir>/
  env/
    <key>.enc                      # default profile
    <profile>/<key>.enc            # additional profiles
  files/
    <key>.enc                      # default profile
    <profile>/<key>.enc            # additional profiles
```

Default-profile paths remain backward compatible; profile-specific entries use subdirectories.

---

## CLI

```
vsync [command] [flags]
```

### Global flags

| Flag | Env var | Description |
|------|---------|-------------|
| `--vault-addr` | `VAULT_ADDR` | Vault address for the default profile |
| `--vault-token` | `VAULT_TOKEN` | Vault token for the default profile |
| `--vault-env-prefix` | `VSYNC_VAULT_ENV_PREFIX` | Override default profile env prefix |
| `--vault-files-prefix` | `VSYNC_VAULT_FILES_PREFIX` | Override default profile file prefix |
| `--vault-kv-version` | `VSYNC_VAULT_KV_VERSION` | Override default profile KV version (1 or 2) |
| `--global-config` | `VSYNC_GLOBAL_CONFIG` | Global config file path |
| `--config` | `VSYNC_CONFIG` | Local override config file path |
| `--key` | `VSYNC_KEY` | Encryption key file path |

Flags override environment, which override config values, which override stored encrypted values.

### `vsync init`

Initialises the state directory, generates the encryption key if needed, and stores credentials for every configured profile.

```
vsync init [--vault-addr ADDR] [--vault-token TOKEN] [--rotate-key]
```

Steps:
1. Create state/cache directories.
2. Generate or load the key file.
3. Load the merged config to discover profiles.
4. For the default profile, resolve address/token from flags → env → config → stored credentials → prompt.
5. For additional profiles, resolve address/token from config → stored credentials → prompt.
6. Encrypt and store credentials per profile.
7. Verify each profile with `LookupSelf()` and warn on short TTLs.

### `vsync shell`

Launches a new interactive shell.

```
vsync shell [--shell /bin/zsh]
```

Steps:
1. Load config and decrypt credentials as needed per profile.
2. Sync all file entries.
3. Generate shims for configured commands.
4. Exec into the requested shell with `PATH=<shims>:$PATH`, `VSYNC_ACTIVE=1`, and `VSYNC_KEY=<key path>`.

Nested shells are rejected when `VSYNC_ACTIVE=1` is already set.

### `vsync exec <command> [args...]`

Internal shim entry-point.

Flags:
- `--dry-run` prints whether the invocation matches the configured filter and which environment variables would be injected, without fetching secrets or execing the real binary.

Steps:
1. Load merged config and find the matching command entry.
2. Evaluate the command `filter` CEL expression against the command arguments, when present.
3. For each variable, resolve its profile, read cache, and fall back to Vault if needed.
4. Merge the fetched variables into the environment.
5. Exec the real binary found later in `PATH` (skipping the shim directory).

### `vsync sync`

Syncs files from Vault to local paths.

```
vsync sync [--file <key>] [--force]
```

Steps:
1. Load merged config.
2. Resolve credentials and Vault profile per file.
3. Skip fresh cache entries unless `--force` is used.
4. Fetch the `content` field, expand `~`, create parent dirs, and write the file with the configured mode.
5. Update encrypted cache with the Vault lease expiry.

### `vsync status`

Prints:
- key file path / existence,
- cache directory,
- default profile Vault address / token TTL,
- configured Vault profiles,
- configured commands with shim presence and cache status,
- file sync entries with filesystem and cache status.

### `vsync cache clear`

```
vsync cache clear [--all | --env | --files | --key <name>]
```

- `--all` clears all cache kinds.
- `--env` / `--files` clear a whole kind.
- `--key` clears a specific key across the default profile and all profile subdirectories.

---

## Cache format

Each cache entry is an encrypted JSON blob:

```json
{
  "value": "<secret>",
  "expires_at": "2026-04-11T12:00:00Z",
  "vault_path": "secret/data/vsync/env/gemini-api-key"
}
```

- `expires_at` is derived from the Vault lease duration minus a 10-second safety margin.
- `lease_duration: 0` means no expiry.
- If Vault is unreachable and a cache entry exists, vsync uses the cached value and warns on stderr.

---

## Security and file modes

- Key, token, and cache files are `0600`.
- Directories are `0700`.
- Shims are `0755`.
- All secret writes go through atomic temp-file-then-rename operations.
- Secrets are never printed.
- The raw Vault token is decrypted only when needed.

---

## Implementation notes

- Language: Go 1.26.
- CLI: Cobra.
- Config: YAML via `gopkg.in/yaml.v3`.
- Vault client: `github.com/hashicorp/vault/api`.
- Crypto: AES-256-GCM from the Go standard library.
- Tooling: `mise.toml` owns the Go toolchain and build/test tasks.

If you add a dependency, update `go.mod` / `go.sum` with `go get` or `go mod tidy`, then run `mise install` if tooling changed.
