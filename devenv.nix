{ pkgs, lib, config, ... }:

# devenv.nix — development environment for vsync integration testing.
#
# This file intentionally only provides a local HashiCorp Vault server
# suitable for integration tests. Go itself is still managed by `mise`
# (see mise.toml), so we don't duplicate the toolchain here.
#
# Usage:
#
#   devenv up                  # starts vault in the foreground (process-compose)
#   # ... in another shell:
#   devenv shell               # enters a shell with VAULT_ADDR/VAULT_TOKEN set
#   vault status               # should report "Initialized: true, Sealed: false"
#   go test ./...              # integration tests can talk to $VAULT_ADDR
#
# The server runs in **dev mode** with a known, fixed root token so that
# tests are fully deterministic. Dev mode keeps all data in memory, so
# every `devenv up` starts from a clean slate — exactly what we want for
# integration testing. Do **not** point production credentials at this.

let
  # Fixed, well-known values for tests. Override via env if desired.
  vaultAddr  = "127.0.0.1:8200";
  vaultToken = "vsync-dev-root-token";
  vaultBin   = "${pkgs.vault-bin}/bin/vault";
in
{
  # Make the `vault` CLI available inside `devenv shell` so tests and
  # ad-hoc debugging can use it directly.
  packages = [ pkgs.vault-bin ];

  # Environment variables auto-exported into `devenv shell` and into any
  # processes started by `devenv up`. These match what `vsync init` and
  # the Vault Go SDK look for, so `go test ./...` will "just work".
  env = {
    VAULT_ADDR       = "http://${vaultAddr}";
    VAULT_TOKEN      = vaultToken;
    VAULT_DEV_ROOT_TOKEN_ID      = vaultToken;
    VAULT_DEV_LISTEN_ADDRESS     = vaultAddr;
  };

  # We deliberately do **not** use `services.vault.enable = true` from
  # devenv, because that module runs a *production-style* server with
  # file storage + manual init/unseal. For integration tests we want
  # dev mode with a predictable root token, so we wire up the process
  # ourselves.
  processes.vault = {
    exec = ''
      set -euo pipefail
      echo "==> starting Vault dev server on http://${vaultAddr}" >&2
      exec ${vaultBin} server -dev \
        -dev-root-token-id="${vaultToken}" \
        -dev-listen-address="${vaultAddr}"
    '';

    # Readiness probe — other processes (and `devenv up --detach`) can
    # wait until vault is actually accepting requests.
    process-compose = {
      readiness_probe = {
        exec.command = "${vaultBin} status -address=http://${vaultAddr}";
        initial_delay_seconds = 1;
        period_seconds = 2;
        timeout_seconds = 2;
        success_threshold = 1;
        failure_threshold = 10;
      };
    };
  };

  # Optional convenience: a scripted seed step that writes a couple of
  # KV v2 secrets matching the default prefixes in internal/config, so
  # integration tests have something to fetch out of the box.
  #
  # Run manually with: `devenv shell -- doctor`
  scripts.doctor.exec = ''
    set -euo pipefail
    export VAULT_ADDR="http://${vaultAddr}"
    export VAULT_TOKEN="${vaultToken}"

    echo "==> vault CLI: ${vaultBin}"
    echo "==> checking vault at $VAULT_ADDR"
    if ! ${vaultBin} status >/dev/null 2>&1; then
      echo "ERROR: Vault is not reachable at $VAULT_ADDR" >&2
      echo "HINT: run 'devenv up' first, then retry this command." >&2
      echo "HINT: if shell realization fails, rerun with 'devenv up -d --verbose' to surface the underlying Nix build error." >&2
      exit 1
    fi

    echo "==> vault is reachable"
  '';

  scripts.seed-vault.exec = ''
    set -euo pipefail
    export VAULT_ADDR="http://${vaultAddr}"
    export VAULT_TOKEN="${vaultToken}"

    echo "==> waiting for vault at $VAULT_ADDR"
    for i in $(seq 1 30); do
      if ${vaultBin} status >/dev/null 2>&1; then
        break
      fi
      if [ "$i" -eq 30 ]; then
        echo "ERROR: Vault did not become ready within 30 seconds at $VAULT_ADDR" >&2
        echo "HINT: check 'devenv processes up --verbose' for the vault server logs." >&2
        exit 1
      fi
      sleep 1
    done

    echo "==> enabling kv v2 at secret/ (already enabled in dev mode, ignore errors)"
    ${vaultBin} secrets enable -path=secret -version=2 kv >/dev/null 2>&1 || true

    echo "==> seeding secret/vsync/env/gemini-api-key"
    if ! ${vaultBin} kv put secret/vsync/env/gemini-api-key \
      value="test-gemini-api-key"; then
      echo "ERROR: failed to seed secret/vsync/env/gemini-api-key" >&2
      exit 1
    fi

    echo "==> seeding secret/vsync/files/example"
    if ! ${vaultBin} kv put secret/vsync/files/example \
      content="hello from vault"; then
      echo "ERROR: failed to seed secret/vsync/files/example" >&2
      exit 1
    fi

    echo "==> done"
  '';

  # Sanity check when entering the shell.
  enterShell = ''
    echo "vsync devenv: VAULT_ADDR=$VAULT_ADDR"
    echo "              VAULT_TOKEN=<redacted, length ''${#VAULT_TOKEN}>"
    echo "              run 'devenv up' to start vault, then 'seed-vault' to populate it"
    echo "              run 'devenv shell -- doctor' for a quick health check"
  '';
}
