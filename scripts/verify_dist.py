import json
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import zipfile

from dist import digest


def verify(targets):
    version = os.environ["VERSION"]
    directory = Path(os.environ["DIST_DIR"]) / version
    host = subprocess.check_output(["go", "env", "GOHOSTOS", "GOHOSTARCH"], text=True).splitlines()
    archives = {}
    for target in targets:
        system, arch = target.split("/")
        suffix = "zip" if system == "windows" else "tar.gz"
        archives[f"datalens_{version}_{system}_{arch}.{suffix}"] = (system, arch)
    assets = set(archives) | {"install.sh"}
    if {p.name for p in directory.iterdir()} != assets | {"SHA256SUMS"}:
        sys.exit("unexpected distribution files")
    if (directory / "install.sh").read_bytes() != Path("install.sh").read_bytes():
        sys.exit("incorrect installer in distribution")
    manifest = "".join(f"{digest(directory / name)}  {name}\n" for name in sorted(assets))
    if (directory / "SHA256SUMS").read_bytes() != manifest.encode("ascii"):
        sys.exit("SHA256SUMS does not match the distribution files")

    with tempfile.TemporaryDirectory() as staging:
        for name, (system, arch) in archives.items():
            windows = system == "windows"
            binary = "datalens.exe" if windows else "datalens"
            members = sorted([binary, "LICENSE", "NOTICES"])
            path = directory / name
            if windows:
                with zipfile.ZipFile(path) as archive:
                    if sorted(archive.namelist()) != members:
                        sys.exit(f"unexpected members in {name}")
                    contents = {key: archive.read(key) for key in members}
            else:
                with tarfile.open(path, "r:gz") as archive:
                    if (sorted(archive.getnames()) != members or not all(m.isfile() for m in archive)
                            or archive.getmember(binary).mode != 0o755):
                        sys.exit(f"unexpected members or executable permissions in {name}")
                    contents = {key: archive.extractfile(key).read() for key in members}
            for key in ["LICENSE", "NOTICES"]:
                if contents[key] != Path(key).read_bytes():
                    sys.exit(f"incorrect {key} in {name}")

            executable = Path(staging) / binary
            executable.write_bytes(contents[binary])
            info = subprocess.check_output(["go", "version", "-m", str(executable)], text=True)
            fields = [line.strip().split("\t") for line in info.splitlines()[1:]]
            for field in [f"GOOS={system}", f"GOARCH={arch}", "CGO_ENABLED=0"]:
                if ["build", field] not in fields:
                    sys.exit(f"missing {field} in {name}")
            if [system, arch] == host:
                executable.chmod(0o755)
                info = json.loads(subprocess.check_output([str(executable), "version", "--format=json"]))
                if (info["version"], info["revision"]) != (version, os.environ["REVISION"]):
                    sys.exit(f"incorrect embedded version or revision in {name}")

    print(f"Verified {len(archives)} archives in {directory}")


if __name__ == "__main__":
    verify(sys.argv[1:])
