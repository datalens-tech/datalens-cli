# DataLens CLI

Use [Conventional Commits for new commits and PR titles](CONTRIBUTING.md#commits-and-pull-request-titles).

## RPC schemas

All installations download the OpenAPI document for the CLI’s pinned API version when creating a profile or running `schema update`. Keep installation exports out of the repository. Use small synthetic schemas to reproduce new schema shapes in tests.

- Check parser changes through `rpc ACTION --help`, `schema show ACTION`, and argument validation before authentication. Validation errors should show a copyable command with the required arguments, placeholders for user values, and any explicit `--profile`. For alternatives, show one complete valid variant.
- Actions missing from a loaded catalog must remain callable. An unsuccessful update must preserve the previous catalog and offline help.
- Run `go test ./internal/rpchelp ./internal/schemacache ./internal/cli` and update affected README examples.
