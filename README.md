# DataLens CLI

Call DataLens RPC actions from the terminal and explore their arguments offline. The CLI supports Yandex Cloud, Yandex Team, and on-premises installations, including Stackland.

## Quick start

Use the command for your platform after the first stable release. To try a published beta, [install a specific version](#install-a-specific-version). You can also [build from source](#build-from-source).

**macOS and Linux**

```sh
curl -fsSL https://github.com/datalens-tech/datalens-cli/releases/latest/download/install.sh | sh
```

**Windows (PowerShell)**

```powershell
([Net.WebClient]::new()).DownloadString('https://github.com/datalens-tech/datalens-cli/releases/latest/download/install.ps1') | Invoke-Expression
```

After installation, run the setup command printed by the installer. If `datalens` is on your `PATH`, use:

```sh
datalens setup
```

Choose your installation and how to supply its token. For Yandex Cloud, you can use a configured Yandex Cloud CLI (`yc iam create-token`) and enter your organization ID. For an on-premises installation, enter its UI address. After choosing a profile name, review the endpoints and authentication settings. Press Enter to create and activate the profile, or type `n` to cancel. To make your first Cloud request, run:

```sh
datalens tenant settings show
```

## Usage

### Look up and call an action

Your installation’s API specification lists the available RPC actions. For Yandex Cloud, see the [DataLens API reference](https://api.datalens.tech/).

`rpc ACTION --help` shows the action description, arguments, and an example when available. `schema show ACTION` prints the full argument schema as JSON when the catalog includes one. Both work offline and use the selected profile’s installation type.

```sh
datalens rpc getWorkbook --help
datalens schema show getWorkbook
datalens rpc getWorkbook '{"workbookId":"your-workbook-id"}'
```

Pass the JSON object after the action name. In bash or zsh, wrap it in single quotes so spaces and double quotes reach the CLI intact. Omit the object to use `{}`. Existing scripts can still pass the object with `--args`.

Before sending a request, the CLI checks the arguments against the selected installation’s bundled schema. On error it shows what to fix and, when available, a minimal example with values you can replace. You can still call actions for which the catalog has no argument schema.

### Profiles and authentication

A profile stores the installation type and endpoints. Create your first profile with `datalens setup` or `datalens profiles create`. The first profile becomes active automatically. Use `--endpoint` for the HTTPS UI address and `--api-endpoint` to override the derived API address.

| Installation type | Default UI | Default API | Credentials |
| --- | --- | --- | --- |
| `cloud` | `https://datalens.ru` | `https://api.datalens.tech` | IAM token and organization ID |
| `yateam` | `https://datalens.yandex-team.ru` | `https://api.datalens.yandex.net` | OAuth token |
| `on-premises` | Supply `--endpoint` | UI address + `/api` | Bearer token or service account key |
| `stackland` | Supply `--endpoint` | UI address + `/api` | Bearer token and organization ID |

For scripts, create the profile with flags. Use `profiles activate` to switch between saved profiles. For a custom UI address, supply the installation type. The API address defaults to the UI address plus `/api`.

```sh
datalens profiles create work --installation-type=cloud --organization=your-organization-id --token-cmd='yc iam create-token'
datalens profiles activate work
```

Profile selection order is `--profile`, `DATALENS_PROFILE`, then the active profile. Cloud and Stackland use the organization saved with `--organization`, or `DATALENS_ORG_ID` if none was saved.

The first configured source supplies the token.

1. `--auth-token` for the current call.
2. The selected profile’s token command or service account key.
3. `DATALENS_OAUTH_TOKEN` for Yandex Team profiles only.
4. `DATALENS_TOKEN`.
5. The file named by `DATALENS_TOKEN_PATH`.

A token command runs once per API call, directly without a shell. Quote arguments containing spaces inside the command string. To select a specific `yc` profile, use e.g. `--token-cmd='yc iam create-token --profile work-cloud'`. The program must exit successfully and print one token to stdout. If it fails, the request stops. The CLI never saves the returned token.

To change a profile’s settings, delete it and create it again. Use `datalens profiles delete work --confirm=yes` in scripts. You can delete any profile, including one named `default`. When you delete the active profile, the first remaining profile in alphabetical order becomes active.

### Scripting

RPC responses go to stdout as indented JSON. Use `--format=json` for compact JSON and structured errors on stderr. The default `--timeout` is 30 seconds and covers both the token command and the request. Interactive setup waits for your answers unless you pass `--timeout`.

Exit codes are `0` for success, `1` for runtime failures, `2` for usage or configuration errors, `3` for authentication failures, and `130` or `143` for SIGINT or SIGTERM.

### Install locations and updates

| Platform | Executable | Directory containing `config.json` |
| --- | --- | --- |
| macOS | `~/.local/bin/datalens` | `~/Library/Application Support/datalens` |
| Linux | `~/.local/bin/datalens` | `$XDG_CONFIG_HOME/datalens` or `~/.config/datalens` |
| Windows | `%LOCALAPPDATA%\Programs\DataLens\bin\datalens.exe` | `%APPDATA%\datalens` |

Set `DATALENS_INSTALL_DIR` or `DATALENS_CONFIG_DIR` to override these locations. Rerun the installer to upgrade to the latest stable release. To install a private release from a checkout, sign in with `gh` and run `sh install.sh` or `.\install.ps1` in PowerShell.

Interactive release builds check for updates at most once a day and print a download link to stderr when an update is available. Stable builds check for stable versions. Beta builds also check for betas. Set `DATALENS_NO_UPDATE_CHECK=1` to disable update checks and notices.

#### Install a specific version

Download the installer from the release you want to test and set `DATALENS_CLI_INSTALL_VERSION` to its tag. For example, once `v0.1.0-beta.1` is published:

```sh
curl -fsSL https://github.com/datalens-tech/datalens-cli/releases/download/v0.1.0-beta.1/install.sh | DATALENS_CLI_INSTALL_VERSION=v0.1.0-beta.1 sh
```

```powershell
$env:DATALENS_CLI_INSTALL_VERSION = 'v0.1.0-beta.1'
([Net.WebClient]::new()).DownloadString("https://github.com/datalens-tech/datalens-cli/releases/download/$env:DATALENS_CLI_INSTALL_VERSION/install.ps1") | Invoke-Expression
Remove-Item Env:DATALENS_CLI_INSTALL_VERSION
```

For a private release, sign in with `gh` and use the same variable with the installer from your checkout.

### On-premises

If you have an access token, set `DATALENS_TOKEN` and create a profile with your installation's UI address.

```sh
datalens profiles create corp --installation-type=on-premises --endpoint=https://datalens.example.com
datalens rpc getWorkbook '{"workbookId":"your-workbook-id"}' --profile=corp
```

For an on-premises service account, provide its ID, key ID, and RSA private key file:

```sh
datalens profiles create automation --installation-type=on-premises \
  --endpoint=https://datalens.example.com --service-account-id=my-service-account \
  --key-id=my-key-id --private-key-file=/path/to/private-key.pem
```

The profile stores the IDs and the file path. When you call an action, the CLI reads the PEM file locally, signs a five-minute JWT, and exchanges it for a bearer token at `<API endpoint>/rpc/exchangeServiceAccountToken`. Use `--api-endpoint` if your API is served at a different address.

Stackland also requires an organization ID. The CLI sends it in `x-dl-org-id` with each request to your deployment.

```sh
datalens profiles create stack --installation-type=stackland --endpoint=https://stack.example.com --organization=your-organization-id
```

### Yandex Team

Export your Yandex Team OAuth token as `DATALENS_OAUTH_TOKEN`. The `yateam` profile type supplies the UI and API addresses automatically. For instructions on obtaining a token, see the [Yandex Team API authentication guide](https://nda.ya.ru/t/TAvihNYX7xMoQm).

```sh
datalens profiles create team --installation-type=yateam
datalens rpc getWorkbook '{"workbookId":"your-workbook-id"}' --profile=team
```

## Development

### Build from source

On macOS or Linux, run these commands from the repository root with Make and the Go version specified in [go.mod](go.mod).

```sh
make build
export PATH="$PWD/bin/$(go env GOOS)-$(go env GOARCH):$PATH"
datalens version
```

Run `make check` for formatting and vet, `make test` for Go tests with the race detector, and `python3 -B scripts/test_install.py` for shell installer tests. CI also installs the packaged Windows binary with PowerShell, runs `datalens version`, and creates a profile.

### Architecture

- [cmd/datalens](cmd/datalens) is the process entry point. [internal/cli](internal/cli) defines the Cobra commands and passes the selected profile and command timeout to the API client.
- [internal/config](internal/config) stores profiles and resolves endpoints. [internal/api](internal/api) obtains the token and sends the RPC request. Each request carries the API version pinned in [internal/buildinfo](internal/buildinfo) in the `x-dl-api-version` header. `datalens version` displays it.
- [internal/rpchelp](internal/rpchelp) embeds the installation-specific catalogs used for action help, schema lookup, argument validation, and minimal examples.

### Refresh the RPC catalogs

Regenerate the Cloud and on-premises catalogs from the SDK snapshots matching the CLI’s pinned API version. To include Yandex Team, append `--yateam-spec /path/to/yateam.json` to the generator command. When changing the pinned API version, refresh all three catalogs together. Update the snapshot link when its source revision changes. [Public SDK snapshots](https://github.com/datalens-tech/datalens-sdk/tree/6e693fa5b40b3cb463a9eb2fb0c5c4a09bae9506/spec)

```sh
python3 scripts/generate_rpc_help.py ../datalens-sdk/spec
go test ./internal/rpchelp ./internal/cli
```

### Release builds

`make build-all` builds macOS (arm64, amd64), Linux (amd64, 386), and Windows (amd64, 386). To build one target, run e.g. `make build TARGET=darwin/amd64`. Binaries go to `bin/<os>-<arch>/`.

Pushing a tag such as `v0.1.0` or `v0.1.0-beta.1` builds the CLI with that version and prepares a draft GitHub Release. Beta tags mark the draft as a prerelease. Review the attached archives, installers, and `SHA256SUMS` before publishing the draft.

To package and verify the same files locally, run the following with Python 3 installed. The files go to `dist/0.1.0/`.

```sh
make dist VERSION=0.1.0
make verify-dist VERSION=0.1.0
```

[Contributing](CONTRIBUTING.md) · [Apache 2.0 license](LICENSE)
