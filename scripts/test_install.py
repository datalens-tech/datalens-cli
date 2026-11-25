import os
from pathlib import Path
import shlex
import subprocess
import tempfile
import unittest

from dist import LEGAL_FILES, digest, pack


ROOT = Path(__file__).resolve().parents[1]
TARGETS = [
    ("Darwin", "arm64", "darwin_arm64"),
    ("Darwin", "x86_64", "darwin_amd64"),
    ("Linux", "x86_64", "linux_amd64"),
    ("Linux", "i686", "linux_386"),
    ("MINGW64_NT-10.0", "x86_64", "windows_amd64"),
    ("MINGW32_NT-10.0", "i686", "windows_386"),
]


class InstallerTest(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="datalens install ")
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.home = self.root / "home"
        self.home.mkdir()
        tools = self.root / "tools"
        tools.mkdir()
        self.assets = self.root / "assets"
        self.assets.mkdir()
        self.config = self.home / ".config" / "datalens"
        self.env = {
            "HOME": str(self.home),
            "TMPDIR": str(self.root),
            "PATH": f"{tools}:{os.defpath}",
            "ASSETS": str(self.assets),
            "APPDATA": str(self.home / "Roaming"),
            "LOCALAPPDATA": str(self.home / "Local"),
        }
        stubs = {
            "uname": 'case "${1:--s}" in -s) echo "$TEST_OS";; -m) echo "$TEST_ARCH";; *) exit 1;; esac',
            "cygpath": 'for path do :; done; printf "%s\\n" "$path"',
            "gh": "exit 1",
            "curl": r'''
out=
url=
while [ "$#" -gt 0 ]; do
    case "$1" in
        -o|--output) out=$2; shift ;;
        https://*) url=$1 ;;
    esac
    shift
done
printf '%s\n' "$url" >> "$ASSETS/requests"
case "$url" in
    https://github.com/datalens-tech/datalens-cli/releases/latest/download/SHA256SUMS)
        file="$ASSETS/SHA256SUMS" ;;
    https://github.com/datalens-tech/datalens-cli/releases/download/v"$TEST_VERSION"/*)
        file="$ASSETS/${url##*/}" ;;
    *) exit 22 ;;
esac
[ -f "$file" ] || exit 22
if [ -n "$out" ]; then cp "$file" "$out"; else cat "$file"; fi
''',
        }
        for name, script in stubs.items():
            tool = tools / name
            tool.write_text("#!/bin/sh\n" + script + "\n")
            tool.chmod(0o755)

    def release(self, version):
        self.env["TEST_VERSION"] = version
        checksums = []
        for _, _, target in TARGETS:
            windows = target.startswith("windows")
            binary = self.root / ("datalens.exe" if windows else "datalens")
            binary.write_text(f"{version} {target}")
            suffix = "zip" if windows else "tar.gz"
            archive = self.assets / f"datalens_{version}_{target}.{suffix}"
            pack(archive, [(binary, 0o755)] + [(ROOT / name, 0o644) for name in LEGAL_FILES])
            checksums.append(f"{digest(archive)}  {archive.name}\n")
        (self.assets / "SHA256SUMS").write_text("".join(sorted(checksums)))

    def install(self, system="Linux", machine="x86_64", *, success=True):
        result = subprocess.run(
            ["sh", str(ROOT / "install.sh")],
            cwd=self.home,
            env={**self.env, "TEST_OS": system, "TEST_ARCH": machine},
            capture_output=True,
            text=True,
            timeout=15,
        )
        if success:
            self.assertEqual(result.returncode, 0, result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0, result.stdout)
            self.assertNotIn("To configure a profile", result.stdout)
        return result.stdout

    def test_release_targets(self):
        self.release("0.2.0")
        for system, machine, target in TARGETS:
            with self.subTest(target=target):
                self.install(system, machine)
                if target.startswith("windows"):
                    exe = Path(self.env["LOCALAPPDATA"]) / "Programs/DataLens/bin/datalens.exe"
                    config = Path(self.env["APPDATA"]) / "datalens"
                else:
                    exe = self.home / ".local/bin/datalens"
                    config = self.home / "Library/Application Support/datalens" if system == "Darwin" else self.config
                self.assertEqual(exe.read_text(), f"0.2.0 {target}")
                self.assertTrue(os.access(exe, os.X_OK))
                self.assertTrue(config.is_dir())
                if not target.startswith("windows"):
                    self.assertEqual(config.stat().st_mode & 0o077, 0)
                self.assertFalse((config / "config.json").exists())

    def test_pinned_release(self):
        requests = self.assets / "requests"
        for version in ("0.1.0", "0.2.0-beta.1"):
            self.release(version)
            for prefix in ("", "v"):
                with self.subTest(version=version, prefix=prefix):
                    self.env["DATALENS_CLI_INSTALL_VERSION"] = prefix + version
                    requests.write_text("")
                    self.install()
                    base = f"https://github.com/datalens-tech/datalens-cli/releases/download/v{version}"
                    self.assertEqual(requests.read_text().splitlines(), [
                        f"{base}/SHA256SUMS", f"{base}/datalens_{version}_linux_amd64.tar.gz",
                    ])
                    self.assertEqual((self.home / ".local/bin/datalens").read_text(), f"{version} linux_amd64")

    def test_pin_rejects_wrong_manifest_and_invalid_versions(self):
        self.release("0.2.0")
        self.env["DATALENS_CLI_INSTALL_VERSION"] = ""
        self.install()
        self.env.update(TEST_VERSION="0.2.0-beta.1", DATALENS_CLI_INSTALL_VERSION="v0.2.0-beta.1")
        requests = self.assets / "requests"
        requests.write_text("")
        self.install(success=False)
        self.assertEqual(requests.read_text().splitlines(), [
            "https://github.com/datalens-tech/datalens-cli/releases/download/v0.2.0-beta.1/SHA256SUMS",
        ])
        self.assertEqual((self.home / ".local/bin/datalens").read_text(), "0.2.0 linux_amd64")
        requests.unlink()
        for invalid in ("latest", "01.0.0", "0.1.0-beta.01", "0.1.0\n0.2.0", "v0.1.0/../latest"):
            with self.subTest(invalid=invalid):
                self.env["DATALENS_CLI_INSTALL_VERSION"] = invalid
                self.install(success=False)
                self.assertFalse(requests.exists())

    def test_upgrade_preserves_config_and_failed_download_preserves_binary(self):
        install_dir = self.home / "user's custom bin"
        config_dir = self.home / "custom config"
        self.env.update(DATALENS_INSTALL_DIR=str(install_dir), DATALENS_CONFIG_DIR=str(config_dir))
        self.release("0.1.0")
        output = self.install()
        exe = install_dir / "datalens"
        self.assertEqual(shlex.split(output.splitlines()[-1]), [str(exe), "setup"])
        config = config_dir / "config.json"
        config.write_text('{"profiles":{}}')
        self.release("0.2.0")
        self.env["PATH"] = f"{install_dir}:{self.env['PATH']}"
        self.assertEqual(self.install().splitlines()[-1].strip(), "datalens setup")
        self.assertEqual(exe.read_text(), "0.2.0 linux_amd64")
        self.release("0.3.0")
        binary = self.root / "datalens"
        binary.write_text("unexpected binary")
        pack(self.assets / "datalens_0.3.0_linux_amd64.tar.gz", [(binary, 0o755)])
        self.install(success=False)
        self.release("0.3.0")
        manifest = self.assets / "SHA256SUMS"
        manifest.write_text(manifest.read_text() * 2)
        self.install(success=False)
        self.assertEqual(exe.read_text(), "0.2.0 linux_amd64")
        manifest.unlink()
        self.install(success=False)
        self.assertEqual(exe.read_text(), "0.2.0 linux_amd64")
        self.assertEqual(config.read_text(), '{"profiles":{}}')

    def test_private_release_via_gh(self):
        self.release("0.2.0")
        tools = self.root / "tools"
        (tools / "curl").write_text("#!/bin/sh\nexit 22\n")
        (tools / "gh").write_text("""#!/bin/sh
[ "$1 $2" = "release download" ] || exit 1
shift 2
tag=
asset=
while [ "$#" -gt 0 ]; do
    case "$1" in
        --repo) [ "$2" = github.com/datalens-tech/datalens-cli ] || exit 1; shift ;;
        --pattern) asset=$2; shift ;;
        --output) [ "$2" = - ] || exit 1; shift ;;
        v*) tag=$1 ;;
        *) exit 1 ;;
    esac
    shift
done
case "$tag/$asset" in
    "${TEST_MANIFEST_TAG:-}/SHA256SUMS"|"v$TEST_VERSION/datalens_${TEST_VERSION}_linux_amd64.tar.gz") cat "$ASSETS/$asset" ;;
    *) exit 1 ;;
esac
""")
        self.install()
        self.assertEqual((self.home / ".local/bin/datalens").read_text(), "0.2.0 linux_amd64")
        self.release("0.3.0-beta.1")
        self.env.update(TEST_MANIFEST_TAG="v0.3.0-beta.1", DATALENS_CLI_INSTALL_VERSION="0.3.0-beta.1")
        self.install()
        self.assertEqual((self.home / ".local/bin/datalens").read_text(), "0.3.0-beta.1 linux_amd64")

    def test_linux_xdg_and_unsupported_target(self):
        self.release("0.2.0")
        self.env["XDG_CONFIG_HOME"] = "relative"
        self.install(success=False)
        self.assertFalse((self.home / "relative").exists())
        self.env["XDG_CONFIG_HOME"] = str(self.home / "xdg")
        self.install("Linux", "aarch64", success=False)
        self.assertFalse((self.home / ".local/bin/datalens").exists())
        self.install()
        self.assertTrue((self.home / "xdg/datalens").is_dir())
        self.assertFalse(self.config.exists())


if __name__ == "__main__":
    unittest.main()
