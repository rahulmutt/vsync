Write a SPEC.md for the following:
- A cli tool `vsync` 
  - which takes VAULT_ADDR and VAULT_TOKEN as env vars (or optionally CLI flags, use good CLI UX principles) and creates a new shell environment that copies the existing environment and generates a new encryption key at ~/.local/state/vsync/keys/* and encrypts the VAULT_ADDR and VAULT_TOKEN as files using that key in ~/.local/state/vsync/tokens/* 
  and reads the config file declared at ~/.config/vsync/config.yaml
  - Example of config:

```yaml
```
    env:
      commands: 
      - name: pi
        variables:
        - name: GEMINI_API_KEY
          key: gemini-api-key
    files:
    - path: ~/.pi/agent/auth.json
      key: pi-agent-auth

    
  This config means that in the new shell environment, the `pi` command is shimmed to fetch the `gemini-api-key` from HashiCorp Vault (the default prefix path should be configurable), set it to the `GEMINI_API_KEY` environment variable and then exec into the `pi` program that is further in the path. Vault connection is done by decrypting the stored tokens above at ~/.local/state/vsync/tokens/* and connecting to vault and getting back the values. Secrets should be cached when possible (taking into account their expiries from vault) and stored encrypted using the key above in ~/.local/state/vsync/tokens/*
  It also automatically syncs the files in the files key from vault, so it fetches `pi-agent-auth` (files vault key prefix should also be configurable) from vault and stores the value directly in the `path` so in this case `~/.pi/agent/auth.json`.
  There should be a separate vault kv prefix for env vars and files.

Implementation details:
- Use `mise.toml` for any tooling dependencies and select the best language for the job.
- Run `mise install` after a tool has been added / updated.

