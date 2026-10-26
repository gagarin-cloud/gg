#!/bin/sh
# Installs gg, the Gagarin CLI, on macOS or Linux.
#
#   curl -fsSL https://gagarin.cloud/install.sh | sh
#
# It downloads the release archive for this machine, checks it against the
# release's checksums.txt, and refuses to install anything that does not match.
# No sudo: gg goes to ~/.local/bin, and this script never edits a shell profile.
# If that directory is not on PATH, it prints the line to add.
#
# Environment:
#   GAGARIN_INSTALL_DIR      where to put gg (default ~/.local/bin)
#   GAGARIN_INSTALL_VERSION  a release to install, e.g. v0.54.0 (default: latest)
#   GAGARIN_RELEASES         where releases live (default the GitHub releases page)
#
# Everything is inside main(), so a download cut off halfway runs nothing.

set -eu

say() { printf '%s\n' "$*"; }
die() {
  printf 'gg install: %s\n' "$*" >&2
  exit 1
}

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | cut -d' ' -f1
  elif command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 -r "$1" | cut -d' ' -f1
  else
    die "found none of sha256sum, shasum or openssl, so the download cannot be verified"
  fi
}

main() {
  command -v curl >/dev/null 2>&1 || die "curl is required"
  command -v tar >/dev/null 2>&1 || die "tar is required"

  releases=${GAGARIN_RELEASES:-https://github.com/gagarin-cloud/gg/releases}
  releases=${releases%/}

  case $(uname -s) in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *) die "this script installs on macOS and Linux. On Windows: irm https://gagarin.cloud/install.ps1 | iex" ;;
  esac

  case $(uname -m) in
    x86_64 | amd64) arch=amd64 ;;
    arm64 | aarch64) arch=arm64 ;;
    *) die "no gg build for $(uname -m); go install github.com/gagarin-cloud/gg@latest builds one" ;;
  esac
  # A shell under Rosetta reports x86_64 on Apple silicon. The native build is
  # the one to have.
  if [ "$os" = darwin ] && [ "$arch" = amd64 ] &&
    [ "$(sysctl -n hw.optional.arm64 2>/dev/null || true)" = 1 ]; then
    arch=arm64
  fi

  if [ -n "${GAGARIN_INSTALL_VERSION:-}" ]; then
    tag=v${GAGARIN_INSTALL_VERSION#v}
  else
    # /releases/latest redirects to /releases/tag/<tag>. Reading the redirect
    # rather than the API sidesteps the API's rate limit for anonymous callers.
    latest=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$releases/latest") ||
      die "cannot reach $releases"
    tag=${latest##*/}
  fi
  case $tag in
    v[0-9]*) ;;
    *) die "cannot tell which release to install (got \"$tag\")" ;;
  esac
  case $tag in
    *[!0-9A-Za-z.-]*) die "\"$tag\" is not a release version" ;;
  esac
  ver=${tag#v}

  asset=gg_${ver}_${os}_${arch}.tar.gz
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT

  say "downloading gg $tag for $os/$arch"
  curl -fsSL -o "$tmp/$asset" "$releases/download/$tag/$asset" ||
    die "cannot download $releases/download/$tag/$asset"
  curl -fsSL -o "$tmp/checksums.txt" "$releases/download/$tag/checksums.txt" ||
    die "cannot download the checksums for $tag"

  expected=$(awk -v a="$asset" '$2 == a { print $1 }' "$tmp/checksums.txt")
  [ -n "$expected" ] || die "checksums.txt for $tag has no entry for $asset; refusing to install"
  actual=$(sha256 "$tmp/$asset")
  [ "$expected" = "$actual" ] ||
    die "$asset does not match its checksum (expected $expected, got $actual); refusing to install"

  mkdir "$tmp/x"
  tar -xzf "$tmp/$asset" -C "$tmp/x"
  [ -f "$tmp/x/gg" ] || die "$asset has no gg binary in it"

  if [ -n "${GAGARIN_INSTALL_DIR:-}" ]; then
    dir=$GAGARIN_INSTALL_DIR
  else
    [ -n "${HOME:-}" ] || die "HOME is not set; set GAGARIN_INSTALL_DIR to say where gg goes"
    dir=$HOME/.local/bin
  fi
  mkdir -p "$dir" || die "cannot create $dir"
  # Copy beside the target and rename over it, so a gg that is running right
  # now is replaced whole rather than overwritten in place.
  cp "$tmp/x/gg" "$dir/.gg.$$" || die "cannot write to $dir"
  chmod 755 "$dir/.gg.$$"
  mv -f "$dir/.gg.$$" "$dir/gg"

  say "installed $("$dir/gg" version) to $dir/gg"

  case ":$PATH:" in
    *":$dir:"*)
      found=$(command -v gg 2>/dev/null || true)
      if [ -n "$found" ] && [ "$found" != "$dir/gg" ]; then
        say ""
        say "warning: $found comes before $dir on PATH, so \`gg\` still runs that one."
        say "  remove it, or move $dir earlier in PATH."
      fi
      ;;
    *)
      say ""
      say "$dir is not on your PATH. Add it with:"
      case ${SHELL:-} in
        */zsh) say "  echo 'export PATH=\"$dir:\$PATH\"' >> ~/.zshrc && exec zsh" ;;
        */bash) say "  echo 'export PATH=\"$dir:\$PATH\"' >> ~/.bashrc && exec bash" ;;
        */fish) say "  fish_add_path $dir" ;;
        *) say "  echo 'export PATH=\"$dir:\$PATH\"' >> ~/.profile" ;;
      esac
      ;;
  esac

  say ""
  say "next: gg login"
}

main "$@"
