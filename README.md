# DataLens CLI

Call DataLens RPC actions from the terminal and explore their arguments offline. The CLI supports Yandex Cloud, Yandex Team, and Enterprise installations.

## Quick start

Once the first stable release is published, install it on macOS, Linux, or Windows (Git Bash) with the following command. Until then, [build from source](#build-from-source).

```sh
curl -fsSL https://github.com/datalens-tech/datalens-cli/releases/latest/download/install.sh | sh
```

The installer checks the download against the release’s SHA-256 checksum and tells you if you need to add its directory to `PATH`. Run `datalens version` to check the installation.

For your first Yandex Cloud request, you’ll need your organization ID and a configured Yandex Cloud CLI (`yc`). Replace `your-organization-id` below. The first command creates a DataLens profile named `work`. The second uses it to fetch your tenant settings as JSON.

```sh
datalens profiles create work --installation-type=cloud --organization=your-organization-id \
  --token-cmd='yc iam create-token'
datalens tenant settings show --profile=work
```

## Usage

### Look up and call an action

`rpc ACTION --help` shows the action description, arguments, and an example when available. `schema show ACTION` prints the full argument schema as JSON when the catalog includes one. Both work offline and use the selected profile’s installation type.

```sh
datalens rpc getWorkbook --help --profile=work
datalens schema show getWorkbook --profile=work
datalens rpc getWorkbook --profile=work --args '{"workbookId":"your-workbook-id"}'
```

Pass arguments as a JSON object. Omitting `--args` sends `{}`. You can also call actions that your server supports but the bundled catalog does not describe.

### Profiles and authentication

A profile stores the installation type and endpoints. The built-in `default` targets Yandex Cloud. Use `--endpoint` for the HTTPS UI address and `--api-endpoint` to override the derived API address.

| Installation type | Default UI | Default API | Credentials |
| --- | --- | --- | --- |
| `cloud` | `https://datalens.ru` | `https://api.datalens.tech` | IAM token and organization ID |
| `yateam` | `https://datalens.yandex-team.ru` | `https://api.datalens.yandex.net` | OAuth token |
| `onpremise-standalone`, `stackland` | Supply `--endpoint` | UI address + `/api` | Bearer token |

For a custom UI address, supply the installation type. The API address defaults to the UI address plus `/api`. To use a profile without passing its name on each call, activate it.

```sh
datalens profiles create corp --installation-type=onpremise-standalone --endpoint=https://datalens.example.com
datalens profiles list
datalens profiles activate corp
```

Profile selection order is `--profile`, `DATALENS_PROFILE`, the active profile, then `default`. Cloud uses the organization saved with `--organization`, or `DATALENS_ORG_ID` if none was saved.

The first configured source supplies the token.

1. `--auth-token` for the current call.
2. The selected profile’s `--token-cmd`.
3. The `DATALENS_TOKEN` environment variable.
4. The file named by `DATALENS_TOKEN_PATH`.

A token command runs once per API call, directly without a shell. Quote arguments containing spaces inside the command string. To select a specific `yc` profile, use e.g. `--token-cmd='yc iam create-token --profile work-cloud'`. The program must exit successfully and print one token to stdout. If it fails, the request stops. The CLI never saves the returned token.

To change a profile’s settings, delete it and create it again. Use `datalens profiles delete work --confirm=yes` in scripts. Deleting the active profile switches back to `default`.

### Scripting

RPC responses go to stdout as indented JSON. Use `--format=json` for compact JSON and structured errors on stderr. The default `--timeout` is 30 seconds and covers both the token command and the request.

Exit codes are `0` for success, `1` for runtime failures, `2` for usage or configuration errors, `3` for authentication failures, and `130` or `143` for SIGINT or SIGTERM.

### Install locations and updates

| Platform | Executable | Directory containing `config.json` |
| --- | --- | --- |
| macOS | `~/.local/bin/datalens` | `~/Library/Application Support/datalens` |
| Linux | `~/.local/bin/datalens` | `$XDG_CONFIG_HOME/datalens` or `~/.config/datalens` |
| Windows (Git Bash, experimental) | `%LOCALAPPDATA%\Programs\DataLens\bin\datalens.exe` | `%APPDATA%\datalens` |

Set `DATALENS_INSTALL_DIR` or `DATALENS_CONFIG_DIR` to override these locations. Rerun the installer to upgrade to the latest stable release. To install a private release from a checkout, sign in with `gh` and run `sh install.sh`.

Interactive release builds check for updates at most once a day and print a download link to stderr when an update is available. Stable builds check for stable versions. Beta builds also check for betas. Set `DATALENS_NO_UPDATE_CHECK=1` to disable update checks and notices.

## Development

### Build from source

On macOS or Linux, run these commands from the repository root with Make and the Go version specified in [go.mod](go.mod).

```sh
make build
export PATH="$PWD/bin/$(go env GOOS)-$(go env GOARCH):$PATH"
datalens version
```

Run `make check` for formatting and vet, `make test` for Go tests with the race detector, and `python3 -B scripts/test_install.py` for installer tests.

### Architecture

- [cmd/datalens](cmd/datalens) is the process entry point. [internal/cli](internal/cli) defines the Cobra commands and passes the selected profile and command timeout to the API client.
- [internal/config](internal/config) stores profiles and resolves endpoints. [internal/api](internal/api) obtains the token and sends the RPC request. Each request carries the API version pinned in [internal/buildinfo](internal/buildinfo) in the `x-dl-api-version` header. `datalens version` displays it.
- [internal/rpchelp](internal/rpchelp) embeds the Cloud, Enterprise, and Yandex Team catalogs used by action help and schema lookup. To regenerate the public catalogs from an SDK checkout, run `python3 scripts/generate_rpc_help.py ../datalens-sdk/spec`. Add `--yateam-spec /path/to/yateam.json` to refresh Yandex Team as well. [Public SDK snapshots](https://github.com/datalens-tech/datalens-sdk/tree/6e693fa5b40b3cb463a9eb2fb0c5c4a09bae9506/spec)

### Release builds

`make build-all` builds macOS (arm64, amd64), Linux (amd64, 386), and Windows (amd64, 386). To build one target, run e.g. `make build TARGET=darwin/amd64`. Binaries go to `bin/<os>-<arch>/`.

To package and verify a release locally, run the following with Python 3 installed. The archives, installer, and `SHA256SUMS` go to `dist/0.1.0/`.

```sh
make dist VERSION=0.1.0
make verify-dist VERSION=0.1.0
```

[Contributing](CONTRIBUTING.md) · [Apache 2.0 license](LICENSE)
