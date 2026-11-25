# DataLens CLI

## Updating RPC schemas

Follow [the catalog refresh procedure](README.md#refresh-the-rpc-catalogs). The generated catalogs in `internal/rpchelp` drive four things: `rpc ACTION --help`, `schema show ACTION`, argument validation before authentication, and the minimal command printed on validation errors.

- Review changed actions in each affected installation. If a snapshot introduces a new schema shape, update the generator or the shared help and example code as needed. Keep these rules in code so they survive the next refresh.
- Check a changed action through both help and validation. The error should show a copyable command with the required arguments, preserve an explicit `--profile`, and use placeholders for user values. For alternative argument shapes, show one complete valid variant. Actions without a bundled schema must remain callable.
- Run `go test ./internal/rpchelp ./internal/cli`. The catalog-wide test validates a generated example for every bundled argument schema. Add a focused regression case when a new shape changes the required fields, nested arguments, or choice of variant. Update affected README examples in the same change.
