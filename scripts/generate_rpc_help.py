import argparse
import json
from pathlib import Path


SCHEMA_REF = "#/components/schemas/"
CATALOG_DIR = Path(__file__).resolve().parents[1] / "internal" / "rpchelp"


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


def catalog(spec):
    components = spec["components"]
    sources = components["schemas"]
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
    parser = argparse.ArgumentParser(description="Generate offline RPC help from the public datalens-tech/datalens-sdk OpenAPI snapshots.")
    parser.add_argument("spec_dir", type=Path, help="datalens-sdk/spec directory")
    args = parser.parse_args()
    for edition, filename in [("cloud", "yacloud.json"), ("enterprise", "enterprise.json")]:
        spec = json.loads((args.spec_dir / filename).read_text(encoding="utf-8"))
        write_catalog(catalog(spec), CATALOG_DIR / f"{edition}.json")


if __name__ == "__main__":
    main()
