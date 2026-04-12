# AGENTS.md — AI Agent Operating Guide for `vsync`

This document tells AI coding agents (Claude, Codex, Gemini, etc.) how to understand,
build, test, and extend this repository. Read it before making any changes.

---

## 1. What This Repo Does

`vsync` is a Go CLI tool that:

1. **Stores** HashiCorp Vault credentials (address + token) encrypted on disk using a
   locally-generated AES-256 key.
2. **Launches** a new interactive shell where configured commands are _shimmed_ so that
   required secrets are fetched from Vault and injected as environment variables at
   exec-time.
3. **Syncs** files from Vault to local paths on shell entry (or on demand).
4. **Caches** secrets encrypted on disk, respecting Vault lease/TTL expiry.

The authoritative design lives in **`SPEC.md`**. When in doubt, defer to `SPEC.md`.

---

## 2. Prerequisites & Setup

### Required tooling

| Tool | Purpose | How to install |
|------|---------|----------------|
| `mise` | Pins the Go version and exposes task shortcuts | <https://mise.jdx.dev> |
| Go 1.26 | The only runtime | Installed automatically by `mise install` |

### First-time setup

```sh
mise install          # installs Go 1.26 and any other tools in mise.toml
go mod download       # fetch all Go dependencies
mise run build        # produces dist/vsync
```

Do **not** manually change `go.mod` / `go.sum`; use `go get` / `go mod tidy` then commit
both files together.

---

## 3. Repository Layout

```
vsync/
├── cmd/vsync/              # main package — CLI wiring only (Cobra commands)
│   ├── main.go             # calls rootCmd().Execute()
│   ├── root.go             # global flags, PersistentPreRunE, helpers (die, resolve*)
│   ├── cmd_init.go         # vsync init
│   ├── cmd_shell.go        # vsync shell
│   ├── cmd_exec.go         # vsync exec  (called by shims)
│   ├── cmd_sync.go         # vsync sync
│   ├── cmd_status.go       # vsync status
│   └── cmd_cache.go        # vsync cache clear
├── internal/
│   ├── config/config.go    # load & validate $XDG_CONFIG_HOME/vsync/config.yaml (fallback ~/.config/vsync/config.yaml)
│   ├── crypto/             # AES-256-GCM key gen, encrypt, decrypt; unit-tested
│   ├── shell/shell.go      # build augmented env, syscall.Exec into shell or command
│   ├── shim/shim.go        # write/remove #!/bin/sh shim scripts
│   ├── state/state.go      # path helpers for resolved vsync state paths; atomic writes
│   └── vault/
│       ├── client.go       # Vault connection, KV v1/v2 GET
│       └── cache.go        # read/write encrypted cache entries
├── dist/                   # build output (gitignored)
├── .github/workflows/      # SLSA3 release pipeline (see §15)
├── .slsa-goreleaser/       # per-platform build configs used by the SLSA builder
├── devenv.nix              # local Vault dev server for integration tests (see §11)
├── go.mod / go.sum
├── mise.toml               # Go version pin + task definitions
├── SPEC.md                 # canonical design spec
├── BOOTSTRAP.md            # original brief that produced SPEC.md
└── AGENTS.md               # ← this file
```

---

## 4. Build, Test & Install

All day-to-day operations go through `mise run <task>`:

| Command | What it does |
|---------|-------------|
| `mise run build` | `go build -o dist/vsync ./cmd/vsync` |
| `mise run test` | `go test ./...` |
| `mise run install` | `go install ./cmd/vsync` (installs to `$GOPATH/bin`) |

> **Always run `mise run build` after editing Go source** to verify compilation.
> Always run `mise run test` before declaring a task complete.

---

## 5. Internal Package Reference

### `internal/state`

Owns **all paths** under the resolved vsync state directory (`VSYNC_STATE_DIR`, `XDG_STATE_HOME/vsync`, or fallback `~/.local/state/vsync`) and cache directory (`VSYNC_CACHE_DIR`, `XDG_CACHE_HOME/vsync`, or fallback `~/.cache/vsync`). Never hard-code those paths elsewhere.

Key API:
```go
dirs, _ := state.DefaultDirs()
dirs.KeyFile()               // <state dir>/keys/default.key
dirs.TokenFile("vault_addr")  // <state dir>/tokens/vault_addr.enc
dirs.CacheFile("env", "gemini-api-key") // <cache dir>/env/gemini-api-key.enc
dirs.ShimFile("pi")          // <state dir>/shims/pi
dirs.EnsureAll()             // create all directories (mode 0700)
state.WriteAtomic(path, data, 0600) // write to tmp then rename
```

### `internal/crypto`

Owns **all cryptographic operations**. AES-256-GCM with a prepended 12-byte nonce and
`additionalData = "vsync/v1"`. Never implement encryption elsewhere.

Key API:
```go
key, _ := crypto.GenerateKey(path)  // generates & persists 32-byte key
key, _ := crypto.LoadKey(path)      // reads key from disk
blob, _ := crypto.Encrypt(key, plaintext)
plain, _ := crypto.Decrypt(key, blob)
crypto.EncryptFile(key, path, plaintext)
plain, _ := crypto.DecryptFile(key, path)
```

### `internal/config`

Loads and validates the global config file at `$XDG_CONFIG_HOME/vsync/config.yaml` (fallback `~/.config/vsync/config.yaml`). Returns a `*config.Config` struct.

Important defaults (applied inside `Load()` if the YAML omits them):
- `vault.env_prefix` → `"secret/data/vsync/env"`
- `vault.files_prefix` → `"secret/data/vsync/files"`
- `vault.kv_version` → `2`

### `internal/vault`

- **`client.go`** — creates a `*vault.Client`, resolves KV path based on `kv_version`,
  and fetches secrets by key name (appends to the configured prefix). Reads `value` for
  env secrets and `content` for file secrets.
- **`cache.go`** — `ReadCache` / `WriteCache` for `CacheEntry` JSON blobs encrypted via
  `internal/crypto`. Cache kind is `"env"` or `"files"`.

### `internal/shim`

Generates minimal `#!/bin/sh` scripts under `dirs.Shims`. Each shim delegates to
`vsync exec <name> "$@"`. Shims are written atomically with mode `0755`.

### `internal/shell`

- `Launch(shellBin, shimDir, keyFile)` — prepends shimDir to PATH, sets `VSYNC_ACTIVE=1`
  and `VSYNC_KEY`, then `syscall.Exec`s the shell (replaces current process).
- `ExecCommand(name, args, extraEnv, shimDir)` — finds the real binary (skipping the shim
  dir), merges `extraEnv` into `os.Environ()`, then `syscall.Exec`s the real command.

---

## 6. Command Reference

### `cmd_init.go` — `vsync init`

1. Creates state directories via `dirs.EnsureAll()`.
2. Generates or loads the encryption key.
3. Resolves `VAULT_ADDR` / `VAULT_TOKEN` (flag → env → interactive prompt).
4. Encrypts both values with `crypto.EncryptFile` into `dirs.TokenFile(...)`.
5. Validates connectivity via `vault.Auth().LookupSelf()`.

### `cmd_shell.go` — `vsync shell`

1. Loads config and decrypts Vault credentials.
2. Calls `vsync sync` logic to sync all `files` entries.
3. Calls `shim.Ensure(dirs, commandNames)` to write/refresh shims.
4. Calls `shell.Launch(...)` — `syscall.Exec` replaces the process.
5. Guards against nested invocations (`VSYNC_ACTIVE=1`).

### `cmd_exec.go` — `vsync exec <command> [args...]`

Called by shims, never directly by users.

1. Loads config; finds the `env.commands` entry matching `<command>`.
2. For each variable: checks encrypted cache, falls back to Vault fetch if expired/missing.
3. Calls `shell.ExecCommand(...)` — `syscall.Exec` replaces the process.

### `cmd_sync.go` — `vsync sync`

Fetches `files` entries from Vault, writes to local `path` (expands `~`), updates cache.
Supports `--file <key>` (specific entry) and `--force` (bypass cache).

### `cmd_status.go` — `vsync status`

Prints: key file path/existence, Vault address (truncated), token validity, shim presence
for each configured command, cache entries with expiry times.

### `cmd_cache.go` — `vsync cache clear`

Deletes encrypted cache files. Flags: `--all`, `--env`, `--files`, `--key <name>`.

---

## 7. Coding Conventions

### Error handling

- All errors bubble up and are printed to `stderr` by the Cobra runner.
- Use `fmt.Errorf("short context: %w", err)` for wrapping — always include the operation
  name so stack traces are readable.
- Prefix user-visible errors with `vsync: ` (the `die()` helper in `root.go` does this).
- **Exit codes**: `0` success · `1` general error · `2` misconfiguration · `3` Vault
  unreachable. Use `os.Exit(N)` sparingly; prefer returning errors from `RunE`.

### Vault unreachability fallback

When Vault is unreachable and a non-expired cache entry exists, log a warning to stderr
and continue with the cached value. Never silently swallow Vault errors when no cache
exists.

### File permissions

All secrets files (keys, tokens, cache) → `0600`.  
Directories → `0700`.  
Shim scripts → `0755`.  
Use `state.WriteAtomic` for all writes (temp file + rename = atomic).

### Sensitive values

- Never log or print secret values. In `--verbose` mode, redact them.
- Never store the raw key or token in any environment variable (only the key *file path*
  is exported as `VSYNC_KEY`).
- Vault credentials are decrypted on-demand and not retained beyond the current operation.

### Importing rules

- `cmd/vsync` may import `internal/*`.
- `internal` packages must **not** import from `cmd/vsync`.
- Cross-package imports within `internal` are fine (e.g. `vault` imports `crypto` and
  `state`).

---

## 8. Adding a New Command

1. Create `cmd/vsync/cmd_<name>.go` with a `func <name>Cmd() *cobra.Command`.
2. Register it in `rootCmd()` in `root.go` via `root.AddCommand(<name>Cmd())`.
3. Keep all business logic in the appropriate `internal/` package.
4. Add a test file if the command has non-trivial logic.
5. Run `mise run build` and `mise run test` to verify.

---

## 9. Adding a New Internal Package

1. Create `internal/<pkg>/<pkg>.go` with `package <pkg>` at the top.
2. Keep the package single-responsibility (one clear domain).
3. Write at least a basic test file `internal/<pkg>/<pkg>_test.go`.
4. Do not add external dependencies without justification — check if the stdlib suffices
   first.

---

## 10. Adding a New Go Dependency

```sh
go get github.com/some/package@latest
go mod tidy
```

Commit `go.mod` and `go.sum` together. Run `mise run build` and `mise run test` before
committing.

---

## 11. Testing

Tests live alongside source (e.g. `internal/crypto/crypto_test.go`).

```sh
mise run test          # runs all tests
go test ./internal/... # run only library tests
go test -run TestFoo ./internal/crypto/  # run a specific test
```

- Use `t.TempDir()` for temporary files (auto-cleaned).
- Unit tests should not require a live Vault server. Mock Vault responses or test only
  the crypto/state/config layers directly.
- For integration tests that need Vault, gate them with `t.Skip()` unless
  `VAULT_ADDR` and `VAULT_TOKEN` are set in the environment.

### Integration testing with `devenv.nix`

The repo ships a `devenv.nix` that runs a local HashiCorp Vault **dev-mode** server
with a fixed, well-known root token so integration tests are fully deterministic.
Dev mode keeps all state in memory, so every `devenv up` starts from a clean slate.

| Knob | Value | Notes |
|------|-------|-------|
| `VAULT_ADDR`  | `http://127.0.0.1:8200` | Auto-exported into `devenv shell` and all child processes. |
| `VAULT_TOKEN` | `vsync-dev-root-token`  | Fixed root token, safe only because dev mode is ephemeral. |
| KV mount      | `secret/` (KV v2)       | Dev mode enables this automatically. |

Typical loop:

```sh
devenv up           # foreground: starts `vault server -dev` via process-compose
# in another terminal
devenv shell        # VAULT_ADDR / VAULT_TOKEN already in env
seed-vault          # seeds secret/vsync/env/* and secret/vsync/files/* samples
go test ./...       # integration tests pick up VAULT_ADDR / VAULT_TOKEN
```

Implementation notes for agents modifying `devenv.nix`:

- We deliberately **do not** use `services.vault.enable = true`. The upstream devenv
  module runs a *production-style* server with file storage and manual init/unseal,
  which is the wrong shape for tests. We use a plain `processes.vault` entry that
  execs `vault server -dev -dev-root-token-id=... -dev-listen-address=...`.
- A process-compose `readiness_probe` runs `vault status` so `devenv up --detach`
  waits until Vault is actually accepting requests.
- The `seed-vault` script writes sample secrets under the default prefixes from
  `internal/config` (`secret/vsync/env/*`, `secret/vsync/files/*`). Keep it in sync
  if those defaults ever change.
- Go itself is **not** pinned in `devenv.nix` — `mise.toml` owns the toolchain.
  Don't duplicate the Go version there.
- Never reuse `vsync-dev-root-token` or any other dev-mode token outside of tests.

---

## 12. Key Data Flow (for orientation)

```
vsync shell
  └─ load config ($XDG_CONFIG_HOME/vsync/config.yaml, fallback ~/.config/vsync/config.yaml)
  └─ decrypt vault creds (tokens/*.enc) with key (keys/default.key)
  └─ sync files from Vault → local paths (<cache dir>/files/*.enc)
  └─ write shims (shims/<cmd>) → #!/bin/sh exec vsync exec <cmd> "$@"
  └─ syscall.Exec(shell) with PATH=shims:$PATH, VSYNC_ACTIVE=1, VSYNC_KEY=...

# Inside the shell, user runs `pi ...`
pi ...
  └─ shims/pi is resolved first in PATH
  └─ shims/pi → exec vsync exec pi "$@"
      └─ find "pi" entry in config.env.commands
      └─ for each variable: check <cache dir>/env/<key>.enc → Vault if expired
      └─ syscall.Exec(real pi binary) with GEMINI_API_KEY=... injected
```

---

## 13. Common Pitfalls

| Pitfall | What to do instead |
|---------|-------------------|
| Hard-coding resolved vsync state paths | Use `state.Dirs` methods |
| Implementing encryption outside `internal/crypto` | Call `crypto.Encrypt` / `crypto.Decrypt` |
| Using `os.WriteFile` for secrets | Use `state.WriteAtomic` with mode `0600` |
| Printing a secret value (even in debug output) | Redact: `"[REDACTED]"` |
| Calling `syscall.Exec` without exhausting the shim-dir from PATH search | Use `shell.ExecCommand` which already skips the shim dir |
| Adding new Cobra commands directly in `root.go` | Create a dedicated `cmd_<name>.go` file |
| Running `vsync shell` inside an existing vsync shell | Guarded by `VSYNC_ACTIVE=1`; return an error |

---

## 14. Environment Variables Reference

| Variable | Set by | Used by |
|----------|--------|---------|
| `VAULT_ADDR` | User / CI | `cmd_init`, `root.go` resolver |
| `VAULT_TOKEN` | User / CI | `cmd_init`, `root.go` resolver |
| `VSYNC_GLOBAL_CONFIG` | User | `root.go` (global config override) |
| `VSYNC_CONFIG` | User | `root.go` (local override config path) |
| `VSYNC_STATE_DIR` | User / CI | `internal/state.DefaultDirs` full override for the vsync state directory |
| `XDG_STATE_HOME` | User / CI | `internal/state.DefaultDirs` parent for the vsync state directory (`$XDG_STATE_HOME/vsync`) |
| `VSYNC_CACHE_DIR` | User / CI | `internal/state.DefaultDirs` full override for the cache directory |
| `XDG_CACHE_HOME` | User / CI | `internal/state.DefaultDirs` parent for the cache directory (`$XDG_CACHE_HOME/vsync`) |
| `VSYNC_KEY` | `vsync shell` at launch | Shims → `vsync exec` to locate the key file |
| `VSYNC_ACTIVE` | `vsync shell` at launch | Prevents nested shell invocations |
| `VAULT_DEV_ROOT_TOKEN_ID` | `devenv.nix` | Consumed by `vault server -dev` in the integration test environment |
| `VAULT_DEV_LISTEN_ADDRESS` | `devenv.nix` | Consumed by `vault server -dev` in the integration test environment |

---

## 15. Release Process

Releases are produced by the **SLSA3 Go builder** via
`.github/workflows/go-ossf-slsa3-publish.yml`. Every release ships:

- Four binaries — `vsync-{linux,darwin}-{amd64,arm64}` — built with `-trimpath`,
  `CGO_ENABLED=0`, and version metadata injected via `-ldflags -X main.version=...`.
- A matching `.intoto.jsonl` Sigstore-signed provenance file per binary.
- A single `SHA256SUMS` manifest covering all binaries (provenance files excluded).

Per-platform build inputs live in `.slsa-goreleaser/<os>-<arch>.yml`. The workflow's
`args` job computes `VERSION`, `COMMIT`, `COMMIT_DATE`, and `TREE_STATE` once and
passes them to every matrix leg via `evaluated-envs`, so every binary in a release
embeds identical metadata.

### Cutting a release

1. Make sure `main` is green and `mise run build && mise run test` pass locally.
2. Tag the commit with a `v`-prefixed semver tag and push it:
   ```sh
   git tag -s v0.2.0 -m "vsync v0.2.0"
   git push origin v0.2.0
   ```
   Pushing a `v*` tag triggers the workflow automatically. Creating a GitHub
   Release from that tag (`release: created`) also triggers it. As a fallback,
   `workflow_dispatch` accepts an existing tag as input for re-runs.
3. Wait for all four `build` matrix legs to finish. They upload binaries and
   `.intoto.jsonl` files directly to the release.
4. The `checksums` job then downloads all `vsync-*` assets, generates
   `SHA256SUMS`, and uploads it to the same release (with `--clobber`, so re-runs
   are idempotent).

### Verifying a published release

Use the pinned `slsa-verifier` from `mise.toml`:

```sh
mise run verify -- v0.2.0              # host os/arch
mise run verify -- v0.2.0 linux-amd64   # explicit platform
```

The task derives the source URI from `git remote get-url origin`, so it works on
forks without edits. Under the hood it runs:

```sh
slsa-verifier verify-artifact vsync-<os>-<arch> \
  --provenance-path vsync-<os>-<arch>.intoto.jsonl \
  --source-uri github.com/<owner>/<repo> \
  --source-tag v0.2.0
```

### Guidelines for agents touching the release pipeline

- **Do not** edit `.slsa-goreleaser/*.yml` and the workflow independently — the
  matrix in `go-ossf-slsa3-publish.yml` must match the set of config files 1:1.
  Adding a new platform means a new `.slsa-goreleaser/<os>-<arch>.yml` **and** a
  new matrix entry.
- Keep `main: ./cmd/vsync` and the `binary: vsync-{{ .Os }}-{{ .Arch }}` naming
  in sync across all four configs. The `checksums` job globs on `vsync-*`, and
  `mise run verify` expects that exact naming.
- Don't bump the trusted builder ref
  (`slsa-framework/slsa-github-generator/.github/workflows/builder_go_slsa3.yml@v2.1.0`)
  without also bumping the corresponding `slsa-verifier` pin in `mise.toml`.
- Version metadata is read from `git describe --tags --always --dirty`. Keep
  `main.version`, `main.commit`, and `main.date` as package-level vars in
  `cmd/vsync/main.go` so the `-ldflags -X` injections keep working.
- Never add build steps that require network access beyond `go mod download` —
  the SLSA3 builder runs in a locked-down environment and will fail otherwise.
