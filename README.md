# vsync

`vsync` gives you a secure, Vault-integrated shell environment.
It stores Vault credentials encrypted on disk, launches a shell with command shims, and fetches secrets at exec-time.

## Highlights

- multiple Vault profiles with independent addresses, tokens, prefixes, and KV versions
- reusable `env_groups` for shared command variable bundles
- per-secret `profile:` selection in config
- encrypted on-disk cache with Vault TTL awareness
- automatic file syncing on shell entry
- shimmed commands that inject environment variables only when needed

---

## How it works

1. `vsync init` creates or loads the local encryption key and stores Vault credentials encrypted on disk.
2. `vsync shell` prepends a shim directory to `PATH` and launches a new shell.
3. Running a shimmed command (for example `pi`) calls `vsync exec pi`.
4. `vsync exec` resolves the configured Vault profile, fetches the secret, injects the environment variable, and `exec`s the real binary.
5. `vsync sync` pulls file secrets from Vault and writes them to their configured paths.
6. Secrets are cached encrypted on disk so repeated use stays fast and Vault outages can fall back to cached values.

---

## Requirements

- HashiCorp Vault
- a Vault token with read access to the configured KV paths
- `mise` if you want to build from source

---

## Quick start

### 1. Create a config file

`~/.config/vsync/config.yaml`:

```yaml
vault:
  env_prefix: "secret/data/vsync/env"
  files_prefix: "secret/data/vsync/files"
  kv_version: 2
  profiles:
    prod:
      addr: "https://vault.prod.example.com"
      token: "hvs.prod-token"
      env_prefix: "secret/data/prod/env"
      files_prefix: "secret/data/prod/files"
      kv_version: 2

env_groups:
  - name: common
    variables:
      - name: OPENAI_API_KEY
        key: openai-api-key
      - name: ANTHROPIC_API_KEY
        key: anthropic-api-key

  - name: prod-only
    variables:
      - name: GEMINI_API_KEY
        key: gemini-api-key
        profile: prod

env:
  commands:
    - name: pi
      filter: 'args.exists(a, a == "--with-secrets")'
      variables:
        - group: common
        - group: prod-only
        - name: LOCAL_API_KEY
          key: local-api-key

files:
  - path: ~/.pi/agent/auth.json
    key: pi-agent-auth
    profile: prod
```

### 2. Store the matching secrets in Vault

```sh
vault kv put secret/vsync/env/openai-api-key value="sk-..."
vault kv put secret/prod/env/gemini-api-key value="sk-prod-..."
vault kv put secret/prod/files/pi-agent-auth content='{"token":"..."}'
```

### 3. Initialise vsync

```sh
vsync init
```

`vsync init` loads the config, prompts for any missing credentials, and stores each profile encrypted on disk.
If you provide `VAULT_ADDR` / `VAULT_TOKEN` or the matching flags, they apply to the default profile.

### 4. Enter the shell

```sh
vsync shell
```

From this shell, configured commands automatically receive their secrets.

---

## Configuration reference

Global config path:

- `$XDG_CONFIG_HOME/vsync/config.yaml`
- or `~/.config/vsync/config.yaml`

`--config` / `VSYNC_CONFIG` point to a local override file. If omitted, `vsync` searches for `vsync.yaml` in the current directory and parent directories. Configs are merged from root-most to leaf-most.

### Config shape

```yaml
vault:
  addr: "http://127.0.0.1:8200"            # default profile bootstrap value
  token: "hvs.default-token"               # default profile bootstrap value
  env_prefix: "secret/data/vsync/env"
  files_prefix: "secret/data/vsync/files"
  kv_version: 2
  profiles:
    prod:
      addr: "https://vault.prod.example.com"
      token: "hvs.prod-token"
      env_prefix: "secret/data/prod/env"
      files_prefix: "secret/data/prod/files"
      kv_version: 2

env_groups:
  - name: common
    variables:
      - name: OPENAI_API_KEY
        key: openai-api-key
      - name: ANTHROPIC_API_KEY
        key: anthropic-api-key

  - name: prod-only
    variables:
      - name: GEMINI_API_KEY
        key: gemini-api-key
        profile: prod

env:
  commands:
    - name: pi
      variables:
        - group: common
        - group: prod-only
        - name: LOCAL_API_KEY
          key: local-api-key

files:
  - path: ~/.pi/agent/auth.json
    key: pi-agent-auth
    profile: prod
    mode: "0600"
```

### Profile rules

- The top-level `vault:` block is the default profile.
- Additional profiles live under `vault.profiles`.
- `env_groups` defines reusable bundles of environment variables.
- Each command variable may either declare a variable directly or reference a group with `group: <name>`.
- Groups may reference other groups, and nested expansion is checked for cycles.
- Duplicate env var names in the expanded result of a group or command are rejected.
- Each secret reference can set `profile: <name>`.
- If `profile` is omitted, the default profile is used.
- `env_prefix`, `files_prefix`, and `kv_version` default per profile if omitted.
- Groups are expanded in order, so later variables in a command can override variables injected from a group.

### Command filters

Each `env.commands` entry may set `filter:` to a CEL expression evaluated against the command arguments as `args` (a `list<string>`).
If the expression returns `true`, secrets are injected as usual.
If it returns `false`, the command runs without any Vault-derived environment variables.

### Vault paths

The vault path is built as:

- env secrets: `<env_prefix>/<key>`
- file secrets: `<files_prefix>/<key>`

The profile only decides which prefix / credentials to use.

---

## Commands

### `vsync init`

Stores Vault credentials encrypted on disk and generates the encryption key.

```sh
vsync init [--vault-addr ADDR] [--vault-token TOKEN] [--rotate-key]
```

- The default profile uses flags, environment variables, config values, any already stored credentials, and prompts.
- Additional profiles use config values, any already stored credentials, and prompts.
- The command verifies each profile with Vault and warns on short token TTLs.

### `vsync shell`

Launches a new interactive shell with shims active.

```sh
vsync shell [--shell /bin/zsh]
```

It syncs files, writes shims, then execs into the shell with `PATH` prefixed by the shim directory.

### `vsync exec <command> [args...]`

Internal entry-point used by shims.

- `--dry-run` shows whether the invocation matches the configured filter and which environment variables would be injected
- resolves the configured command
- evaluates the command `filter` CEL expression against the command arguments, when present
- chooses the profile for each variable
- reads the encrypted cache or fetches from Vault
- execs the real binary with the fetched environment variables

### `vsync sync`

Manually syncs file secrets to local paths.

```sh
vsync sync
vsync sync --file pi-agent-auth
vsync sync --force
```

### `vsync status`

Shows:
- key file path and existence
- default-profile Vault address and token TTL
- configured profiles
- command and file cache status
- shim presence

### `vsync cache clear`

```sh
vsync cache clear --all
vsync cache clear --env
vsync cache clear --files
vsync cache clear --key gemini-api-key
```

This clears cache entries for the default profile and any profile-specific cache directories.

---

## Storage layout

```text
${XDG_CONFIG_HOME:-$HOME/.config}/vsync/config.yaml

<state dir>/
  keys/default.key
  tokens/vault_addr.enc
  tokens/vault_token.enc
  tokens/<profile>/vault_addr.enc
  tokens/<profile>/vault_token.enc
  shims/<command>

<cache dir>/
  env/<key>.enc
  env/<profile>/<key>.enc
  files/<key>.enc
  files/<profile>/<key>.enc
```

State defaults to `~/.local/state/vsync`; cache defaults to `~/.cache/vsync`.

---

## Caching

vsync caches secrets encrypted on disk so that:

- repeated commands are fast,
- Vault outages can fall back to cached values,
- TTL expiry is respected using Vault lease duration minus a small safety margin.

If Vault is unavailable and a cached value exists, vsync warns and uses the cache.

---

## Security notes

- The encryption key never leaves disk except in memory while vsync is running.
- Vault tokens are decrypted only when needed.
- Secret values are never printed.
- Key/token/cache files are `0600`; directories are `0700`; shims are `0755`.
- Atomic writes are used for secret material.

---

## Building from source

```sh
mise install
mise run build
mise run test
```

`mise.toml` owns the pinned Go toolchain and build/test tasks.

---

## Troubleshooting

### `vsync: vault credentials for profile "..." not found; run 'vsync init' first`

Run `vsync init` again. Make sure you're using the same state directory and config file that defined the profile.

### `vsync: already inside a vsync shell; nested shells are not supported`

Exit the current vsync shell before running `vsync shell` again.

### A shimmed command is using the wrong binary

Make sure the real binary appears later in `PATH` than the shim directory.

---

## Example release/build flow

```sh
mise install
mise run build
mise run test
```

That is usually all you need for local development.
