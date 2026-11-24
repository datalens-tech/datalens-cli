# DataLens CLI

Use `datalens` to call DataLens RPC actions and inspect Cloud tenant settings. The first API request uses Yandex Cloud by default.

## Build

Use the Go version in `go.mod` and Make. By default, `make build` builds for the current Go target. On Apple Silicon:

```sh
make build
./bin/darwin-arm64/datalens help
```

The executable is written to `bin/<os>-<arch>/datalens` (`datalens.exe` on Windows). To rebuild `bin/darwin-amd64/datalens` for Intel Macs, run `make build TARGET=darwin/amd64`. Add the executable's directory to your PATH to use the examples below.

```sh
make build VERSION=0.1.0 REVISION="$(git rev-parse HEAD)" TARGET=linux/amd64
make build-all
```

The build matrix covers macOS arm64/amd64, Linux amd64/386, and Windows amd64/386. Windows runtime support is experimental.

### Distribution archives

Use Python 3, Go, and Make to build and verify a local distribution:

```sh
make dist VERSION=0.1.0
make verify-dist VERSION=0.1.0
```

`dist/0.1.0/` contains six archives and `SHA256SUMS`. Each archive contains the executable, `LICENSE`, and `NOTICES`. Unix targets use `.tar.gz` and Windows targets use `.zip`. Verification checks archive contents, checksums, and the Go build target of every binary. On a matching host it also runs the archived CLI and checks its version and revision.

For local development the version defaults to `dev`. A repeated build with identical artifacts reuses the directory. To compare different builds of the same version, set `DIST_DIR` to a fresh directory for both commands.

### Update notifications

After a successful interactive command, release builds check GitHub Releases in the background at most once a day. When a later command finds a newer version in the local cache, it prints a download link to stderr once for that version. Stable builds check for stable releases. Beta builds also check for newer betas.

Set `DATALENS_NO_UPDATE_CHECK=1` to disable checks and notices. The CLI skips both for help, development builds, CI, redirected input or output, `--non-interactive`, and `--format=json`.

## Make a request

For Yandex Cloud, supply an existing IAM token and your organization ID. For example, in a shell with `yc` already configured:

```sh
export DATALENS_TOKEN="$(yc iam create-token)"
export DATALENS_ORG_ID='your-organization-id'
datalens tenant settings show
```

The shell runs `yc`; the CLI uses the supplied token. You can instead set `DATALENS_TOKEN_PATH` to a file containing the token. Token precedence is `--auth-token`, then `DATALENS_TOKEN`, then `DATALENS_TOKEN_PATH`. Tokens apply only to the current process and are never saved to a profile.

`tenant settings show` calls `getTenantDetails` for a Cloud profile. Run `datalens rpc` for general help. Use `datalens rpc ACTION --help` to look up an action's description, example call, and JSON argument schema when available. Help works offline and uses the installation type stored in the selected profile. Cloud profiles use the Cloud catalog. Standalone and Stackland profiles use the Enterprise catalog. The public binary has no bundled Yandex Team catalog. You can call an RPC action by name on any installation, including actions missing from its catalog.

```sh
datalens rpc getWorkbook --help
datalens rpc getTenantDetails --format=json
datalens rpc getWorkbook --args '{"workbookId":"your-workbook-id"}' --format=json
```

Every RPC request sends the API version embedded in the CLI in the `x-dl-api-version` header. Run `datalens version` to see which version your binary targets.

To call an action, pass its name and optionally supply a JSON object with `--args` (default `{}`). API responses retain the server's JSON structure, indented for reading or compact with `--format=json`. The caller is responsible for the effects of the requested action.

## Switch installations

The built-in `default` profile targets Yandex Cloud and works before any configuration file exists. To use another installation, create a named profile:

```sh
datalens profiles create --name=team --installation-type=yateam
datalens profiles create --name=corp --installation-type=onpremise-standalone \
  --endpoint=https://datalens.corp.example.com
datalens profiles list
datalens profiles activate team
```

`--endpoint` is the UI address. `--api-endpoint` overrides the API address derived from it. Both belong to the profile.

| UI endpoint | Default API endpoint | Installation type |
| --- | --- | --- |
| `https://datalens.ru` | `https://api.datalens.tech` | `cloud` |
| `https://datalens.yandex-team.ru` | `https://api.datalens.yandex.net` | `yateam` |
| Custom, e.g. `https://example.com/datalens` | `https://example.com/datalens/api` | Supply `--installation-type` |

The known installations can also be selected by type alone. A custom UI endpoint requires an explicit type. Endpoints use HTTPS. Yandex Team accepts an OAuth token. Cloud, standalone, and Stackland use bearer tokens appropriate to their installation. Only Cloud requests use an organization ID. Save it at creation with `--organization=ID`, change it with `datalens profiles update NAME --organization=ID`, or supply `DATALENS_ORG_ID` when the selected Cloud profile omits it.

Use `--profile=NAME` for a single command, or set `DATALENS_PROFILE` for a shell session. Selection order is the flag, the environment variable, the active profile, then `default`. Creating a named profile leaves the active selection in place.

```sh
datalens rpc getWorkbook --help --profile=corp
datalens profiles delete corp
# In scripts
datalens profiles delete corp --confirm=yes
```

Deletion prompts in an interactive terminal. Non-interactive and JSON invocations require `--confirm=yes`. Deleting the active profile restores the default selection. You can customize the built-in profile with `profiles create --name=default`; deleting the saved override restores the built-in Cloud profile.

## Configuration

Profiles are stored in `config.json` in the platform configuration directory:

| Platform | Default directory |
| --- | --- |
| macOS | `~/Library/Application Support/datalens` |
| Linux | `$XDG_CONFIG_HOME/datalens`, or `~/.config/datalens` |
| Windows | `%AppData%/datalens` |

`DATALENS_CONFIG_DIR` overrides the full directory. The CLI uses schema version 2, which stores UI and API endpoints separately. If you used the local prototype, move its version 1 config aside and recreate your profiles with `datalens profiles create`.

On Unix, new directories and files use modes `0700` and `0600`. The store checks permissions and replaces the file atomically. Concurrent mutations fail with `CONFIG_LOCKED`. If a process is killed while writing, check that no CLI command is running before removing the stale `.config.lock` file.

## Output and errors

Successful commands write results to stdout. With `--format=json`, failures write an error document to stderr and set a nonzero exit code:

```json
{"error":{"code":"PROFILE_NOT_FOUND","message":"The selected profile does not exist."}}
```

| Exit code | Meaning |
| --- | --- |
| 0 | Success |
| 1 | Runtime, API, or output failure |
| 2 | Usage or configuration error |
| 3 | Authentication failure |
| 130 | Interrupted by SIGINT |
| 143 | Interrupted by SIGTERM |

`--timeout` defaults to `30s`. Use `--debug` for version, command name, and interaction mode. API commands put these diagnostics on stderr so stdout can be piped to a JSON parser. Help, version, and profile management work offline.

## Development

```sh
make check
make test
make build-all
```

Tests use temporary configuration directories, injected environment values, and local HTTP fixtures. CI runs formatting, vet, and race tests on macOS and Linux and verifies distribution archives for all six targets. Release publication is not configured yet.

RPC help uses the public API v3 specifications from the DataLens SDK. To refresh the bundled catalog from an SDK checkout, run `python3 scripts/generate_rpc_help.py ../datalens-sdk/spec` and review the generated changes. [SDK specification snapshot](https://github.com/datalens-tech/datalens-sdk/tree/6e693fa5b40b3cb463a9eb2fb0c5c4a09bae9506/spec)

See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution requirements and [LICENSE](LICENSE) for Apache 2.0 licensing.
