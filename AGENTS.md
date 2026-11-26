# DataLens CLI

## Updating RPC schemas

Follow [the catalog refresh procedure](README.md#refresh-the-rpc-catalogs). The generated catalogs in `internal/rpchelp` drive four things: `rpc ACTION --help`, `schema show ACTION`, argument validation before authentication, and the minimal command printed on validation errors.

- Review changed actions in each affected installation. If a snapshot introduces a new schema shape, update the generator or the shared help and example code as needed. Keep these rules in code so they survive the next refresh.
- Check a changed action through both help and validation. The error should show a copyable command with the required arguments, preserve an explicit `--profile`, and use placeholders for user values. For alternative argument shapes, show one complete valid variant. Actions without a bundled schema must remain callable.
- Run `go test ./internal/rpchelp ./internal/cli`. The catalog-wide test validates a generated example for every bundled argument schema. Add a focused regression case when a new shape changes the required fields, nested arguments, or choice of variant. Update affected README examples in the same change.

## Offline connection schemas

To reproduce the bundled Enterprise and Team connection schemas offline, export the same component maps that the UI merges into Swagger. This example uses Node.js 24 and the UI revision used for these catalogs.

```sh
schema_dir=$(mktemp -d)
ui_revision=a858413a36ae9066c9223229adee0fee3a04bc9c
ui_schemas=projects/yandex/src/shared/schema/bi/openapi
for edition in enterprise yt; do
  git -C ../datalens-ui-private show "$ui_revision:$ui_schemas/bi-openapi-schemas-$edition.ts" > "$schema_dir/$edition.mts"
  node --input-type=module -e '
    const [path, edition] = process.argv.slice(1)
    const schemas = (await import(path))["BI_OPENAPI_SCHEMAS_" + edition.toUpperCase()]
    console.log(JSON.stringify(schemas))
  ' "$schema_dir/$edition.mts" "$edition" > "$schema_dir/$edition.json"
done
python3 scripts/generate_rpc_help.py ../datalens-sdk/spec \
  --enterprise-api "$schema_dir/enterprise.json" \
  --yateam-spec /path/to/yateam.json --yateam-api "$schema_dir/yt.json"
```
