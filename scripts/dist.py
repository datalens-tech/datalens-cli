import gzip
import hashlib
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import zipfile


def pack(archive, files):
    if archive.suffix == ".zip":
        with zipfile.ZipFile(archive, "w") as bundle:
            for path, mode in files:
                entry = zipfile.ZipInfo(path.name)
                entry.create_system = 3
                entry.external_attr = (0o100000 | mode) << 16
                entry.compress_type = zipfile.ZIP_DEFLATED
                with path.open("rb") as source, bundle.open(entry, "w") as target:
                    shutil.copyfileobj(source, target)
        return

    with archive.open("wb") as output:
        with gzip.GzipFile(filename="", fileobj=output, mode="wb", mtime=0) as compressed:
            with tarfile.open(fileobj=compressed, mode="w|") as bundle:
                for path, mode in files:
                    entry = tarfile.TarInfo(path.name)
                    entry.size = path.stat().st_size
                    entry.mode = mode
                    with path.open("rb") as source:
                        bundle.addfile(entry, source)


def digest(path):
    h = hashlib.sha256()
    with path.open("rb") as source:
        for block in iter(lambda: source.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def build(targets):
    version = os.environ["VERSION"]
    revision = os.environ["REVISION"]
    if not re.fullmatch(r"[a-zA-Z0-9][a-zA-Z0-9.+-]*", version):
        sys.exit("VERSION must be a filename-safe version, for example 0.1.0")
    if not re.fullmatch(r"[a-zA-Z0-9][a-zA-Z0-9._+-]*", revision):
        sys.exit("REVISION must be a source revision, for example a Git commit hash")

    dist = Path(os.environ["DIST_DIR"])
    dist.mkdir(parents=True, exist_ok=True)
    destination = dist / version
    with tempfile.TemporaryDirectory(dir=dist) as staging:
        staging = Path(staging).resolve()
        assets = staging / "assets"
        assets.mkdir()
        checksums = []
        for target in sorted(targets):
            system, arch = target.split("/")
            windows = system == "windows"
            binary = staging / ("datalens.exe" if windows else "datalens")
            subprocess.run([os.environ["MAKE"], "build", f"TARGET={target}",
                            f"OUTPUT={binary}", f"VERSION={version}", f"REVISION={revision}"],
                           check=True)
            suffix = "zip" if windows else "tar.gz"
            archive = assets / f"datalens_{version}_{system}_{arch}.{suffix}"
            pack(archive, [(binary, 0o755), (Path("LICENSE"), 0o644), (Path("NOTICES"), 0o644)])
            checksums.append(f"{digest(archive)}  {archive.name}\n")

        installer = assets / "install.sh"
        shutil.copyfile("install.sh", installer)
        installer.chmod(0o755)
        checksums.append(f"{digest(installer)}  {installer.name}\n")

        manifest = assets / "SHA256SUMS"
        manifest.write_bytes("".join(checksums).encode("ascii"))
        try:
            assets.rename(destination)
        except OSError:
            if not destination.is_dir():
                raise
            if (set(destination.iterdir()) != {destination / p.name for p in assets.iterdir()}
                    or any(digest(destination / p.name) != digest(p) for p in assets.iterdir())):
                sys.exit(f"{destination} already contains different artifacts; choose another DIST_DIR")
        print(destination / manifest.name)


if __name__ == "__main__":
    build(sys.argv[1:])
