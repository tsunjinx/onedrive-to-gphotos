#!/bin/bash
# Source launcher for macOS. Does not change system Go or shell profiles.
set -euo pipefail
umask 077
fail() { printf 'od2gp setup: %s\n' "$*" >&2; exit 1; }
root=$(cd "$(dirname "$0")/.." && pwd)
[[ $(uname -s) == Darwin ]] || fail 'This launcher supports macOS only; use od2gp.ps1 on Windows.'
case $(uname -m) in arm64) arch=arm64 ;; x86_64) arch=amd64 ;; *) fail 'Unsupported Mac architecture.' ;; esac
read -r version platform target archive checksum < <(awk -v arch="$arch" '$2=="darwin" && $3==arch {print; exit}' "$root/scripts/toolchain.tsv") || fail 'Missing toolchain manifest entry.'
[[ ${version:-} =~ ^[0-9]+\.[0-9]+\.[0-9]+$ && ${checksum:-} =~ ^[a-f0-9]{64}$ ]] || fail 'Invalid toolchain manifest.'
[[ $archive == "go$version.darwin-$arch.tar.gz" ]] || fail 'Invalid archive name.'
export GOTOOLCHAIN=local
compatible() {
  local output actual
  output=$("$1" version 2>/dev/null) || return 1
  actual=$(printf '%s\n' "$output" | awk '{print $3}')
  [[ $actual =~ ^go[0-9]+\.[0-9]+\.[0-9]+$ ]] || return 1
  [[ $output == *"darwin/$arch" ]] || return 1
  awk -v a="${actual#go}" -v b="$version" 'BEGIN {split(a,x,"."); split(b,y,"."); for(i=1;i<=3;i++){if(x[i]+0>y[i]+0)exit 0;if(x[i]+0<y[i]+0)exit 1}exit 0}'
}
go_bin=$(command -v go || true)
cache=${OD2GP_TOOL_DIR:-"$HOME/Library/Caches/od2gp/toolchains"}
managed="$cache/go$version-darwin-$arch"
stage=''; locked=0; build_dir=''
cleanup() {
  # Only generated directories owned by this invocation are removed.
  [[ -z $stage ]] || rm -rf -- "$stage"
  [[ -z $build_dir ]] || rm -rf -- "$build_dir"
  if [[ $locked == 1 ]]; then rmdir "$cache/.install-lock" 2>/dev/null || true; fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
if [[ -z $go_bin ]] || ! compatible "$go_bin"; then
  go_bin="$managed/go/bin/go"
  if ! compatible "$go_bin"; then
    for tool in curl tar shasum mktemp; do command -v "$tool" >/dev/null || fail "Required utility missing: $tool"; done
    mkdir -p "$cache"
    # Fail closed on a competing/stale lock; never delete another launcher's lock.
    mkdir "$cache/.install-lock" 2>/dev/null || fail "Another setup is active (or interrupted): $cache/.install-lock. Retry after it exits; remove an abandoned empty lock only after checking no setup is running."
    locked=1
    if ! compatible "$go_bin"; then
      [[ ! -e $managed && ! -L $managed ]] || fail 'Managed installation is invalid. Move it aside before retrying.'
      stage=$(mktemp -d "$cache/.install.XXXXXXXX")
      printf 'Installing Go %s locally for macOS %s...\n' "$version" "$arch" >&2
      curl --fail --location --proto '=https' --proto-redir '=https' --connect-timeout 15 --max-time 600 --retry 2 --output "$stage/archive.tar.gz" "https://go.dev/dl/$archive" || fail 'Download failed. Internet access is required for first-time setup; rerun to retry.'
      actual=$(shasum -a 256 "$stage/archive.tar.gz" | awk '{print $1}')
      [[ $actual == "$checksum" ]] || fail 'Go archive checksum mismatch; refusing to extract or execute it.'
      mkdir "$stage/unpacked"
      tar -xzf "$stage/archive.tar.gz" -C "$stage/unpacked" || fail 'Extraction failed; check available disk space.'
      compatible "$stage/unpacked/go/bin/go" || fail 'Downloaded toolchain failed validation.'
      mv "$stage/unpacked" "$managed"
    fi
    rmdir "$cache/.install-lock"
    locked=0
  fi
fi
# A unique output avoids races between concurrent launches/builds.
mkdir -p "$root/.cache"
export GOPATH=${GOPATH:-"$root/.cache/gopath"}
export GOCACHE=${GOCACHE:-"$root/.cache/go-build"}
build_dir=$(mktemp -d "$root/.cache/launch.XXXXXXXX")
cd "$root"
"$go_bin" build -trimpath -o "$build_dir/od2gp" ./cmd/od2gp || fail 'Build failed; the CLI was not started.'
set +e
"$build_dir/od2gp" "$@"
result=$?
set -e
exit "$result"
