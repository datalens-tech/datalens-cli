#!/bin/sh
set -eu

fail() {
    printf 'Error: %s\n' "$*" >&2
    exit 1
}

download() {
    asset=$1
    tag=$2
    destination=$3
    if [ -n "$tag" ]; then
        route="download/$tag"
        set -- "$tag"
    else
        route="latest/download"
        set --
    fi
    if command -v curl >/dev/null 2>&1 &&
        curl -q -fsSL --proto '=https' --proto-redir '=https' -o "$destination" "$releases/$route/$asset" 2>"$tmp/curl-error"; then
        return
    fi
    if command -v gh >/dev/null 2>&1 &&
        GH_PROMPT_DISABLED=1 gh release download "$@" --repo "github.com/$repo" --pattern "$asset" --output - > "$destination"; then
        return
    fi
    if [ -s "$tmp/curl-error" ]; then cat "$tmp/curl-error" >&2; fi
    fail "Could not download $asset. Check that a stable release is published and accessible. For a private repository, sign in with gh."
}

main() {
    [ "$#" -eq 0 ] || fail 'Usage: sh install.sh (set DATALENS_INSTALL_DIR to choose the binary directory)'
    repo=datalens-tech/datalens-cli
    releases="https://github.com/$repo/releases"
    system=$(uname -s)
    machine=$(uname -m)
    case "$system" in
        Darwin)
            os=darwin
            config_dir=${DATALENS_CONFIG_DIR:-"$HOME/Library/Application Support/datalens"}
            ;;
        Linux)
            os=linux
            config_dir=${DATALENS_CONFIG_DIR:-"${XDG_CONFIG_HOME:-$HOME/.config}/datalens"}
            if [ -z "${DATALENS_CONFIG_DIR:-}" ]; then
                case "$config_dir" in /*) ;; *) fail 'XDG_CONFIG_HOME must be an absolute path.' ;; esac
            fi
            ;;
        MINGW*|MSYS*|CYGWIN*) os=windows ;;
        *) fail "No DataLens CLI release for $system/$machine." ;;
    esac
    case "$os/$machine" in
        darwin/arm64|darwin/aarch64) arch=arm64 ;;
        */x86_64|*/amd64) arch=amd64 ;;
        linux/i[3-6]86|windows/i[3-6]86) arch=386 ;;
        *) fail "No DataLens CLI release for $system/$machine." ;;
    esac

    binary=datalens
    ext=tar.gz
    if [ "$os" = windows ]; then
        binary=datalens.exe
        ext=zip
        install_dir=$(cygpath -u "${DATALENS_INSTALL_DIR:-${LOCALAPPDATA:?LOCALAPPDATA is required}/Programs/DataLens/bin}")
        config_dir=$(cygpath -u "${DATALENS_CONFIG_DIR:-${APPDATA:?APPDATA is required}/datalens}")
    else
        install_dir=${DATALENS_INSTALL_DIR:-"$HOME/.local/bin"}
    fi
    case "$install_dir" in /*) ;; *) fail 'The installation directory must be an absolute path.' ;; esac

    tmp=$(mktemp -d "${TMPDIR:-/tmp}/datalens-install.XXXXXXXX")
    stage=''
    trap 'rm -rf "$tmp"; if [ -n "$stage" ]; then rm -f "$stage"; fi' EXIT
    trap 'exit 1' HUP INT TERM
    download SHA256SUMS '' "$tmp/manifest"
    entry=$(LC_ALL=C awk -v target="${os}_${arch}" -v ext="$ext" '
        BEGIN {
            suffix = (ext == "zip" ? "[.]zip" : "[.]tar[.]gz")
            version = "v?[0-9]+[.][0-9]+[.][0-9]+([+][0-9A-Za-z.-]+)?"
            pattern = "^datalens_" version "_" target suffix "$"
        }
        $2 ~ pattern {
            if (NF != 2 || length($1) != 64 || $1 ~ /[^0-9a-fA-F]/) exit 1
            count++
            entry = tolower($1) " " $2
        }
        END { if (count != 1) exit 1; print entry }
    ' "$tmp/manifest") || fail "Expected exactly one stable $os/$arch archive in SHA256SUMS."
    checksum=${entry%% *}
    archive=${entry#* }
    version=${archive#datalens_}
    version=${version%%_*}
    tag=v${version#v}
    download "$archive" "$tag" "$tmp/archive"

    if command -v sha256sum >/dev/null 2>&1; then
        digest=$(sha256sum < "$tmp/archive")
    else
        digest=$(shasum -a 256 < "$tmp/archive")
    fi
    [ "${digest%% *}" = "$checksum" ] || fail "SHA-256 mismatch for $archive."

    mkdir -p "$install_dir"
    target="$install_dir/$binary"
    if [ -L "$target" ] || { [ -e "$target" ] && [ ! -f "$target" ]; }; then
        fail "Expected a regular file at $target."
    fi
    stage=$(mktemp "$install_dir/.datalens-install.XXXXXXXX")
    if [ "$os" = windows ]; then
        unzip -p "$tmp/archive" "$binary" > "$stage"
    else
        tar -xzOf "$tmp/archive" "$binary" > "$stage"
    fi
    [ -s "$stage" ] || fail "Empty executable in $archive."
    chmod 755 "$stage"
    [ ! -L "$config_dir" ] || fail "The configuration directory is a symbolic link: $config_dir"
    (umask 077 && mkdir -p "$config_dir")
    mv -f "$stage" "$target"
    stage=''
    printf 'Installed DataLens CLI %s to %s\n' "$tag" "$target"
    case ":${PATH:-}:" in
        *":$install_dir:"*) ;;
        *) printf 'Add this directory to your PATH: %s\n' "$install_dir" ;;
    esac
}

main "$@"
