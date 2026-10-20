# Named contexts in this fork

Build `feature/named-contexts-v1` from [Amine's fork](https://github.com/amineamanzou/termdevtools/tree/feature/named-contexts-v1). This branch incorporates upstream v0.7.2, including its completion fixes and module-version reporting. The upstream release binaries do not include named contexts.

```sh
git clone --branch feature/named-contexts-v1 https://github.com/amineamanzou/termdevtools.git
cd termdevtools
go build -o termdevtools .
./termdevtools --context lab-opensearch
```

Use Go 1.25 or later with automatic toolchain download enabled, or install the Go toolchain specified in `go.mod` before building offline. On Windows, build `termdevtools.exe` instead.

## Create an inventory

Put `contexts.yaml` in `$XDG_CONFIG_HOME/termdevtools`, or `~/.config/termdevtools` when `XDG_CONFIG_HOME` is unset. This location also applies on macOS and Windows. The program reads this file; switching contexts never rewrites it. Global preferences and connection history stay in `config.yaml`.

These two contexts connect to the local lab clusters when they are running:

```yaml
contexts:
  - name: lab-opensearch
    url: http://127.0.0.1:19200
    auth_type: none
    proxy: none
    labels:
      env: lab
      engine: opensearch

  - name: lab-elasticsearch
    url: http://127.0.0.1:19201
    auth_type: none
    proxy: none
    labels:
      env: lab
      engine: elasticsearch
```

For Basic authentication with a private CA:

```yaml
contexts:
  - name: prod-eu
    url: https://es-prod-eu.example.net:9200
    auth_type: basic
    username: investigator
    password_env: ES_PROD_PASSWORD
    tls:
      verify: true
      ca_file: /etc/company/search-ca.pem
    labels:
      client: example
      env: prod
      region: eu
```

Replace the hostname and CA path with yours. Set `ES_PROD_PASSWORD` in the launching process, or leave it unset to enter the password in the terminal.

| Field | Meaning |
|---|---|
| `name` | Required, unique and case sensitive. 1–64 ASCII characters matching `[A-Za-z0-9][A-Za-z0-9._-]*`. |
| `url` | Required HTTP/HTTPS URL with a host. A path prefix is allowed; credentials, query parameters and fragments are rejected. |
| `auth_type` | `none` (default), `basic`, `api_key`, `bearer` or `mtls`. |
| `username` | Required for Basic authentication. |
| `api_key_id` | API key identifier; omit it for an encoded key, or enter it when prompted. |
| `password_env` | Environment variable holding a Basic password. |
| `api_key_secret_env` | Environment variable holding the raw API key secret or encoded key. |
| `bearer_token_env` | Environment variable holding a Bearer token. |
| `key_passphrase_env` | Environment variable holding an encrypted mTLS key's passphrase. |
| `tls.verify` | Server certificate verification; defaults to `true`. |
| `tls.ca_file` | Private CA file, used for verified HTTPS. |
| `tls.client_cert`, `tls.client_key` | Client certificate and key files, required with an HTTPS URL for `mtls`. |
| `proxy` | HTTP, SOCKS5 or SOCKS5H proxy URL, `none` to bypass proxies, or omitted to use environment proxy settings. Proxy credentials in the URL are rejected. |
| `distribution`, `version` | Optional engine/version detection overrides, as in `config.yaml`. |
| `labels` | Optional map of text keys and text values, shown in the picker and included in its search. Quote numeric-looking values. |

The `_env` fields contain variable **names**, matching `[A-Za-z_][A-Za-z0-9_]*`. Only fields for the selected authentication type are accepted. Plaintext secret fields such as `password` are rejected. Duplicate names or YAML keys, unknown fields, incorrect types and invalid definitions reject the whole inventory. Ordinary YAML aliases are supported; merge keys (`<<`) and cyclic aliases are rejected. A missing or empty file, or `contexts: []`, gives an empty inventory.

## Select and switch

Without `--context`, the picker opens without contacting any cluster. `--context prod-eu` selects that exact name. An unknown name or invalid inventory fails before any network request. `--version` needs no configuration; `--context` cannot be combined with `--export-defaults`.

| Key | Action |
|---|---|
| `F6` | Open the picker; press again to focus its filter. |
| Type in the filter | Search names, URLs and `key=value` labels locally. All words must match; matching ignores case. |
| `↑` / `↓`, then `Enter` | Select and connect. |
| `Esc` | Cancel the attempt and return to the active cluster. |
| `F7` | Reload the inventory, active variables and reference files. |
| `Ctrl+Shift+←/→` | Resize the panels. `F5` still shrinks the editor; `F6` now opens contexts. |

The header always shows the active name and detected engine/version. The old session stays active while the candidate's credentials, connection and files are checked. On success, unsaved requests are saved before the target changes. A connection, file or save failure keeps the old session. Successful switching clears the result panel and cancels old requests and completion lookups; late results cannot update the new session.

Selecting the unchanged active context closes the picker without reconnecting. The picker also offers historical connections and the existing connection form. With an empty named inventory, these connections appear directly.

## Secrets

Secrets are resolved only for the selected context. A nonempty environment variable wins; otherwise a previously entered and successfully validated secret is reused from process memory, or a masked prompt opens. The mTLS passphrase is requested only when the key requires it. An encoded API key includes its identifier; a raw secret needs an identifier too.

Canceled or failed attempts are not cached. A 401/403 clears manually cached credentials for that attempt; there is no automatic retry. A wrong mTLS key passphrase also clears the manual cache, so the next explicit selection can prompt again after key rotation at the same path. Changing connection settings or removing the context clears its cache at reload. Changing labels keeps it. Two names using the same URL have separate caches.

Resolved credentials are not written to configuration or state files, diagnostics, or the F9 curl command. Secrets you put in request text or response data follow the existing editor/export behavior.

## Requests and variables

Each name has its own directory:

```text
<config directory>/contexts/<SHA-256 of "named:" + name>/queries.txt
<config directory>/contexts/<SHA-256 of "named:" + name>/variables.txt
```

`Ctrl+S` in the editor saves requests. Switching saves modified requests; returning restores the buffer and cursor, and reads variables again. Variables use one `name=value` per line, with blank lines and `#` comments allowed. Names match `[A-Za-z_][A-Za-z0-9_]*`; duplicate names and malformed lines are errors. Use `${name}` in requests. A missing variables file means no variables.

Named contexts do not copy or delete the historical `queries_<URL>.txt` and `variables_<URL>.txt` files. Those files remain available through historical connections. Two names on the same URL keep separate requests and variables.

## Change the inventory

Edit `contexts.yaml`, then press `F7`. Invalid YAML leaves the last valid inventory and active session in place. Reload reports errors from variables and reference files too.

| Change | Effect after reload |
|---|---|
| Labels | Picker updates; active connection stays open. |
| URL/authentication/TLS/proxy/engine settings | Active entry is marked changed. Select it to reconnect; its buffer stays attached to its name. |
| Remove the active name | It stays visible as removed and remains usable until you leave it. It cannot be selected again from the removed entry. |
| Rename a context | Creates a new identity and workspace. Old files stay on disk; there is no automatic migration. |

Reload never reconnects automatically. It cancels pending connection attempts only after a valid inventory is loaded. It does not replace the request editor with an externally edited queries file.
