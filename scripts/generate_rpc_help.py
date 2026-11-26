import argparse
import json
import re
from pathlib import Path


SCHEMA_REF = "#/components/schemas/"
INTERNAL_DIR = Path(__file__).resolve().parents[1] / "internal"
CATALOG_DIR = INTERNAL_DIR / "rpchelp"


def local_ref(ref, prefix):
    if not ref.startswith(prefix):
        raise ValueError(f"unsupported OpenAPI reference: {ref}")
    return ref[len(prefix):].replace("~1", "/").replace("~0", "~")


def schema_with_refs(schema):
    dependencies = set()

    def translate(ref):
        dependencies.add(local_ref(ref, SCHEMA_REF))
        return "#/$defs/" + ref[len(SCHEMA_REF):]

    def walk(node):
        if isinstance(node, list):
            return [walk(item) for item in node]
        if not isinstance(node, dict):
            return node
        result = {}
        for key, value in node.items():
            if key == "$ref":
                result[key] = translate(value)
            elif (key == "additionalProperties" and value is False and node.get("properties") == {}
                  and "discriminator" in node and "oneOf" in node):
                # These OpenAPI wrappers declare their properties only inside the selected variant.
                result["unevaluatedProperties"] = False
            elif key == "discriminator" and isinstance(value, dict) and "mapping" in value:
                result[key] = {**value, "mapping": {k: translate(v) for k, v in value["mapping"].items()}}
            elif key in ("properties", "patternProperties", "$defs", "dependentSchemas"):
                result[key] = {name: walk(child) for name, child in value.items()}
            elif key in ("default", "enum", "const", "examples", "example"):
                result[key] = value
            else:
                result[key] = walk(value)
        return result

    return walk(schema), dependencies


def request_example(media, schema, components):
    if isinstance(media.get("example"), dict):
        return media["example"]
    for example in media.get("examples", {}).values():
        if "$ref" in example:
            key = local_ref(example["$ref"], "#/components/examples/")
            example = components["examples"][key]
        if isinstance(example.get("value"), dict):
            return example["value"]
    if isinstance(schema, dict):
        for example in [schema.get("example"), *schema.get("examples", [])]:
            if isinstance(example, dict):
                return example
    return None


def connection_components(path, version):
    source = json.loads(path.read_text(encoding="utf-8"))
    if "openapi" in source:
        if source["info"]["version"] != version:
            raise ValueError(f"{path}: expected API version {version!r}, got {source['info']['version']!r}")
        source = source["components"]["schemas"]
    components = {}
    pending = {"ConnectionCreate", "ConnectionUpdate"}
    while pending:
        name = pending.pop()
        if name not in source:
            raise ValueError(f"{path}: missing connection schema {name!r}")
        components[name] = source[name]
        _, refs = schema_with_refs(source[name])
        pending.update(refs - components.keys())
    return components


def catalog(spec, connections=None):
    components = spec["components"]
    sources = components["schemas"] if connections is None else {**components["schemas"], **connections}
    schemas = {}
    dependencies = {}
    actions = {}
    for path, route in spec["paths"].items():
        if not path.startswith("/rpc/") or "post" not in route:
            continue
        operation = route["post"]
        action = {}
        description = operation.get("description") or operation.get("summary")
        if description:
            action["description"] = description
        actions[path[len("/rpc/"):]] = action

        body = operation.get("requestBody", {})
        if "$ref" in body:
            body = components["requestBodies"][local_ref(body["$ref"], "#/components/requestBodies/")]
        media = body.get("content", {}).get("application/json", {})
        schema = media.get("schema")
        if isinstance(schema, dict) and schema.keys() == {"$ref"}:
            schema = sources[local_ref(schema["$ref"], SCHEMA_REF)]
        example = request_example(media, schema, components)
        if example is not None:
            action["example"] = example
        if schema is None:
            continue

        action["schema"], pending = schema_with_refs(schema)
        needed = set()
        while pending:
            name = pending.pop()
            if name in needed:
                continue
            needed.add(name)
            if name not in schemas:
                schemas[name], dependencies[name] = schema_with_refs(sources[name])
            pending.update(dependencies[name] - needed)
        if needed:
            action["definitions"] = sorted(needed)
    return {"actions": actions, "schemas": schemas}


def write_catalog(data, path):
    with path.open("w", encoding="utf-8", newline="\n") as out:
        out.write("{\n")
        for i, (section, entries) in enumerate(data.items()):
            if i:
                out.write(",\n")
            out.write(f'  "{section}": {{\n')
            for j, (key, value) in enumerate(sorted(entries.items())):
                if j:
                    out.write(",\n")
                item = json.dumps(value, ensure_ascii=False, separators=(",", ":"), sort_keys=True)
                out.write(f"    {json.dumps(key)}: {item}")
            out.write("\n  }")
        out.write("\n}\n")


def main():
    parser = argparse.ArgumentParser(description="Generate offline RPC help from DataLens SDK OpenAPI snapshots.")
    parser.add_argument("spec_dir", type=Path, help="datalens-sdk/spec directory")
    parser.add_argument("--yateam-spec", type=Path, help="also refresh the Yandex Team catalog from this SDK OpenAPI snapshot")
    parser.add_argument("--enterprise-api", type=Path, required=True, help="Enterprise Swagger JSON or exported BI schema components")
    parser.add_argument("--yateam-api", type=Path, help="Yandex Team Swagger JSON or exported BI schema components")
    args = parser.parse_args()
    if bool(args.yateam_spec) != bool(args.yateam_api):
        parser.error("--yateam-spec and --yateam-api must be supplied together")
    source = (INTERNAL_DIR / "buildinfo" / "buildinfo.go").read_text(encoding="utf-8")
    match = re.search(r'^const APIVersion = "([0-9]+)"$', source, re.MULTILINE)
    if match is None:
        parser.error("cannot read the pinned API version from buildinfo.go")
    expected_version = match.group(1)
    catalogs = {}
    sources = {"cloud": args.spec_dir / "yacloud.json", "enterprise": args.spec_dir / "enterprise.json"}
    if args.yateam_spec is not None:
        sources["yateam"] = args.yateam_spec
    apis = {"enterprise": args.enterprise_api, "yateam": args.yateam_api}
    for edition, path in sources.items():
        spec = json.loads(path.read_text(encoding="utf-8"))
        version = spec.get("info", {}).get("version")
        if version != expected_version:
            parser.error(f"{path}: expected API version {expected_version!r}, got {version!r}")
        try:
            connections = connection_components(apis[edition], expected_version) if edition in apis else None
        except ValueError as exc:
            parser.error(str(exc))
        catalogs[edition] = catalog(spec, connections)
    for edition, data in catalogs.items():
        write_catalog(data, CATALOG_DIR / f"{edition}.json")


if __name__ == "__main__":
    main()
