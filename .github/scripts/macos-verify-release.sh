#!/bin/bash
# Verify that a macOS release was signed by the fixed AnyPic signing
# certificate, not ad hoc:
#
#   * the .app passes codesign --verify --deep --strict;
#   * its signing certificate SHA-1 equals .github/macos-signing-cert.pem;
#   * its designated requirement names com.anypic.desktop and pins the same
#     certificate (leaf, or root for a self-signed certificate);
#   * a .dmg is signed by the same certificate, verifies, and holds an app
#     that passes the checks above.
#
# Usage: macos-verify-release.sh path/to/AnyPic.app [path/to/AnyPic.dmg ...]
# Exits non-zero on any failed check.

set -uo pipefail

repo_root=$(cd "$(dirname "$0")/../.." && pwd)
expected_cert=${MACOS_SIGNING_CERT:-$repo_root/.github/macos-signing-cert.pem}
identifier=${MACOS_BUNDLE_ID:-com.anypic.desktop}

if [ ! -f "$expected_cert" ]; then
  echo "error: expected signing certificate not found: $expected_cert" >&2
  exit 2
fi
if [ "$#" -eq 0 ]; then
  echo "usage: $0 path/to/AnyPic.app [path/to/AnyPic.dmg ...]" >&2
  exit 2
fi

expected_sha1=$(/usr/bin/openssl x509 -in "$expected_cert" -outform der \
  | /usr/bin/shasum -a 1 | /usr/bin/awk '{print toupper($1)}')
expected_sha1_lower=$(printf '%s' "$expected_sha1" | tr '[:upper:]' '[:lower:]')
status=0
tmp_dirs=()
mounted_mnt=""

cleanup() {
  local rc=$?
  trap - EXIT INT TERM HUP
  if [ -n "$mounted_mnt" ]; then
    hdiutil detach "$mounted_mnt" >/dev/null 2>&1 || true
  fi
  if [ "${#tmp_dirs[@]}" -gt 0 ]; then
    for dir in "${tmp_dirs[@]}"; do
      rm -rf "$dir"
    done
  fi
  exit "$rc"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

pass() { printf 'PASS: %s: %s\n' "$1" "$2"; }
fail() {
  printf 'FAIL: %s: %s\n' "$1" "$2"
  status=1
}
fingerprint_der() {
  /usr/bin/openssl x509 -inform der -in "$1" -outform der \
    | /usr/bin/shasum -a 1 | /usr/bin/awk '{print toupper($1)}'
}

verify_bundle() {
  local app=$1 label=$2
  local tmp dr sha1
  if [ ! -d "$app" ]; then
    fail "$label" "app bundle not found: $app"
    return
  fi

  if codesign --verify --deep --strict "$app" >/dev/null 2>&1; then
    pass "$label" "codesign --verify --deep --strict"
  else
    fail "$label" "codesign --verify --deep --strict failed"
  fi

  tmp=$(mktemp -d)
  tmp_dirs+=("$tmp")
  if codesign -d --extract-certificates="$tmp/cert" "$app" >/dev/null 2>&1 \
    && [ -f "$tmp/cert0" ]; then
    sha1=$(fingerprint_der "$tmp/cert0")
    if [ "$sha1" = "$expected_sha1" ]; then
      pass "$label" "signing certificate SHA-1 $sha1"
    else
      fail "$label" "signing certificate SHA-1 $sha1, expected $expected_sha1"
    fi
  else
    fail "$label" "no signing certificate (ad-hoc or unsigned)"
  fi
  rm -rf "$tmp"

  dr=$(codesign -d -r- "$app" 2>&1 || true)
  if printf '%s\n' "$dr" | grep -Fq "identifier \"$identifier\""; then
    pass "$label" "designated requirement identifier \"$identifier\""
  else
    fail "$label" "designated requirement does not name \"$identifier\": $dr"
  fi
  if printf '%s\n' "$dr" | grep -Eiq "certificate (leaf|root) = H\"$expected_sha1_lower\""; then
    pass "$label" "designated requirement pins certificate $expected_sha1"
  else
    fail "$label" "designated requirement does not pin certificate $expected_sha1: $dr"
  fi
}

verify_dmg() {
  local dmg=$1 label=$2
  local mnt tmp app sha1
  if [ ! -f "$dmg" ]; then
    fail "$label" "disk image not found: $dmg"
    return
  fi

  tmp=$(mktemp -d)
  tmp_dirs+=("$tmp")
  if codesign -d --extract-certificates="$tmp/cert" "$dmg" >/dev/null 2>&1 \
    && [ -f "$tmp/cert0" ]; then
    sha1=$(fingerprint_der "$tmp/cert0")
    if [ "$sha1" = "$expected_sha1" ]; then
      pass "$label" "disk image signing certificate SHA-1 $sha1"
    else
      fail "$label" "disk image signing certificate SHA-1 $sha1, expected $expected_sha1"
    fi
  else
    fail "$label" "disk image has no signing certificate (ad-hoc or unsigned)"
  fi
  rm -rf "$tmp"

  if codesign --verify --strict "$dmg" >/dev/null 2>&1; then
    pass "$label" "codesign --verify --strict"
  else
    fail "$label" "codesign --verify --strict failed"
  fi

  mnt=$(mktemp -d)
  tmp_dirs+=("$mnt")
  if ! hdiutil attach -readonly -nobrowse -mountpoint "$mnt" "$dmg" >/dev/null 2>&1; then
    fail "$label" "cannot mount disk image"
    return
  fi
  mounted_mnt="$mnt"
  app=$(find "$mnt" -maxdepth 2 -type d -name '*.app' | head -n 1)
  if [ -z "$app" ]; then
    fail "$label" "disk image contains no .app"
  else
    verify_bundle "$app" "$label (app inside)"
  fi
  hdiutil detach "$mnt" >/dev/null 2>&1 || fail "$label" "cannot detach disk image"
  mounted_mnt=""
}

for path in "$@"; do
  case "$path" in
    *.app) verify_bundle "$path" "$(basename "$path")" ;;
    *.dmg|*.DMG) verify_dmg "$path" "$(basename "$path")" ;;
    *) fail "$(basename "$path")" "not an .app or .dmg" ;;
  esac
done

if [ "$status" -ne 0 ]; then
  printf 'Signature verification failed.\n' >&2
  exit 1
fi
printf 'Signature verification passed for %s.\n' "$*"
