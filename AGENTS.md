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
| Go 1.25 | The only runtime | Installed automatically by `mise install` |

### First-time setup

```sh
mise install          # installs Go 1.25 and any other tools in mise.toml
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
│   ├── config/config.go    # load & validate ~/.config/vsync/config.yaml
│   ├── crypto/             # AES-256-GCM key gen, encrypt, decrypt; unit-tested
│   ├── shell/shell.go      # build augmented env, syscall.Exec into shell or command
│   ├── shim/shim.go        # write/remove #!/bin/sh shim scripts
│   ├── state/state.go      # path helpers for ~/.local/state/vsync/*; atomic writes
│   └── vault/
│       ├── client.go       # Vault connection, KV v1/v2 GET
│       └── cache.go        # read/write encrypted cache entries
├── dist/                   # build output (gitignored)
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

Owns **all paths** under `~/.local/state/vsync/`. Never hard-code those paths elsewhere.

Key API:
```go
dirs, _ := state.DefaultDirs()
dirs.KeyFile()              // ~/.local/state/vsync/keys/default.key
dirs.TokenFile("vault_addr") // ~/.local/state/vsync/tokens/vault_addr.enc
dirs.CacheFile("env", "gemini-api-key") // .../cache/env/gemini-api-key.enc
dirs.ShimFile("pi")         // ~/.local/state/vsync/shims/pi
dirs.EnsureAll()            // create all directories (mode 0700)
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

Loads and validates `~/.config/vsync/config.yaml`. Returns a `*config.Config` struct.

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
- Tests should not require a live Vault server. Mock Vault responses or test only the
  crypto/state/config layers directly.
- For integration tests that need Vault, gate them with `t.Skip()` unless
  `VAULT_ADDR` and `VAULT_TOKEN` are set in the environment.

---

## 12. Key Data Flow (for orientation)

```
vsync shell
  └─ load config (~/.config/vsync/config.yaml)
  └─ decrypt vault creds (tokens/*.enc) with key (keys/default.key)
  └─ sync files from Vault → local paths (cache/files/*.enc)
  └─ write shims (shims/<cmd>) → #!/bin/sh exec vsync exec <cmd> "$@"
  └─ syscall.Exec(shell) with PATH=shims:$PATH, VSYNC_ACTIVE=1, VSYNC_KEY=...

# Inside the shell, user runs `pi ...`
pi ...
  └─ shims/pi is resolved first in PATH
  └─ shims/pi → exec vsync exec pi "$@"
      └─ find "pi" entry in config.env.commands
      └─ for each variable: check cache/env/<key>.enc → Vault if expired
      └─ syscall.Exec(real pi binary) with GEMINI_API_KEY=... injected
```

---

## 13. Common Pitfalls

| Pitfall | What to do instead |
|---------|-------------------|
| Hard-coding `~/.local/state/vsync` paths | Use `state.Dirs` methods |
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
| `VSYNC_CONFIG` | User | `root.go` (config path override) |
| `VSYNC_KEY` | `vsync shell` at launch | Shims → `vsync exec` to locate the key file |
| `VSYNC_ACTIVE` | `vsync shell` at launch | Prevents nested shell invocations |
