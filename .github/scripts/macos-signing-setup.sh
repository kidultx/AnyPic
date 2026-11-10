#!/bin/bash
# Import the AnyPic release signing certificate into a temporary keychain.
#
# The certificate is a fixed self-signed identity ("AnyPic Release Signing"),
# so every release has the same designated requirement and macOS keeps TCC
# grants such as Screen Recording across upgrades. The public certificate in
# .github/macos-signing-cert.pem is the expected identity: the imported p12
# must contain exactly that certificate, or this script fails.
#
# Inputs (CI sets these from repository secrets):
#   MACOS_SIGNING_P12          base64 of the p12 (or MACOS_SIGNING_P12_FILE)
#   MACOS_SIGNING_PASSWORD     password of the p12 (or MACOS_SIGNING_PASSWORD_FILE)
#
# Options:
#   --allow-adhoc   With no secrets, warn and build ad-hoc instead of failing.
#                   Non-tag workflow_dispatch runs pass this; tag builds do not.
#
# On success MACOS_SIGNING_IDENTITY, MACOS_SIGNING_MODE=certificate and
# MACOS_SIGNING_KEYCHAIN are exported to later GitHub Actions steps through
# GITHUB_ENV (when set) and printed for local use. The p12 and its password
# stay out of the log.

set -euo pipefail

allow_adhoc=0
for arg in "$@"; do
  case "$arg" in
    --allow-adhoc) allow_adhoc=1 ;;
    *)
      echo "usage: $0 [--allow-adhoc]" >&2
      exit 2
      ;;
  esac
done

repo_root=$(cd "$(dirname "$0")/../.." && pwd)
expected_cert=${MACOS_SIGNING_CERT:-$repo_root/.github/macos-signing-cert.pem}
keychain=${MACOS_SIGNING_KEYCHAIN:-${RUNNER_TEMP:-${TMPDIR:-/tmp}}/anypic-signing.keychain-db}
keychain=${keychain%/}
state_file="$keychain.search-list"
keychain_password_file="$keychain.password"
p12_file="$keychain.p12"
imported_cert="$keychain.imported.pem"
script_dir=$(cd "$(dirname "$0")" && pwd)

cleanup_on_failure() {
  local rc=$?
  trap - EXIT INT TERM HUP
  if [ "$rc" -ne 0 ]; then
    MACOS_SIGNING_KEYCHAIN="$keychain" "$script_dir/macos-signing-cleanup.sh" || true
  fi
  exit "$rc"
}
trap cleanup_on_failure EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

log() { printf '%s\n' "$*"; }
fail() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}
emit_env() {
  if [ -n "${GITHUB_ENV:-}" ]; then
    printf '%s=%s\n' "$1" "$2" >> "$GITHUB_ENV"
  fi
}

if [ ! -f "$expected_cert" ]; then
  fail "$expected_cert: expected signing certificate not found"
fi

if [ -n "${MACOS_SIGNING_P12:-}" ]; then
  p12_base64=$MACOS_SIGNING_P12
elif [ -n "${MACOS_SIGNING_P12_FILE:-}" ]; then
  p12_base64=$(cat "$MACOS_SIGNING_P12_FILE")
else
  p12_base64=""
fi
if [ -n "${MACOS_SIGNING_PASSWORD:-}" ]; then
  password=$MACOS_SIGNING_PASSWORD
elif [ -n "${MACOS_SIGNING_PASSWORD_FILE:-}" ]; then
  password=$(cat "$MACOS_SIGNING_PASSWORD_FILE")
else
  password=""
fi
# A secret copied from a file can keep that file's trailing newline. Neither
# the base64 p12 nor its password contains CR or LF (openssl reads only the
# first line of a -passout file:), so drop them on both input paths.
p12_base64=$(printf '%s' "$p12_base64" | tr -d '\r\n')
password=$(printf '%s' "$password" | tr -d '\r\n')

if [ -z "$p12_base64" ] || [ -z "$password" ]; then
  if [ "$allow_adhoc" -eq 1 ]; then
    printf '::warning::MACOS_SIGNING_P12 / MACOS_SIGNING_PASSWORD are not set; building ad-hoc. macOS Screen Recording permission will be lost on upgrade.\n'
    printf 'MACOS_SIGNING_MODE=adhoc\n'
    emit_env MACOS_SIGNING_MODE adhoc
    exit 0
  fi
  printf '::error::tag builds require the MACOS_SIGNING_P12 and MACOS_SIGNING_PASSWORD secrets; refusing to ship an ad-hoc signed build.\n' >&2
  exit 1
fi

# Keep the previous search list so cleanup can put it back exactly.
security list-keychains -d user > "$state_file"

# A stale keychain from an interrupted run is never reused.
security delete-keychain "$keychain" 2>/dev/null || true
rm -f "$p12_file" "$keychain_password_file" "$imported_cert"

umask 077
/usr/bin/openssl rand -hex 24 > "$keychain_password_file"
keychain_password=$(cat "$keychain_password_file")

# --timestamp and hardened runtime (used by mygo for real identities) need
# codesign to reach the private key without user interaction.
security create-keychain -p "$keychain_password" "$keychain"
security set-keychain-settings -lu "$keychain"
security unlock-keychain -p "$keychain_password" "$keychain"

printf '%s' "$p12_base64" | /usr/bin/openssl base64 -d -A -out "$p12_file"
security import "$p12_file" -k "$keychain" -P "$password" \
  -T /usr/bin/codesign -T /usr/bin/security >/dev/null
security set-key-partition-list -S apple-tool:,apple: -s -k "$keychain_password" "$keychain" >/dev/null
rm -f "$p12_file"

# Put the keychain in the user search list for mygo's codesign calls.
search_args=("$keychain")
while IFS= read -r line; do
  line=${line#"${line%%[![:space:]]*}"}
  line=${line%\"}
  line=${line#\"}
  if [ -n "$line" ]; then
    search_args+=("$line")
  fi
done < "$state_file"
security list-keychains -d user -s "${search_args[@]}"

# The imported certificate must be exactly the one committed to the repo.
security find-certificate -a -c "AnyPic Release Signing" -p "$keychain" > "$imported_cert"
cert_count=$(grep -c 'BEGIN CERTIFICATE' "$imported_cert" || true)
if [ "$cert_count" -ne 1 ]; then
  fail "expected one imported certificate, found $cert_count"
fi
fingerprint() {
  /usr/bin/openssl x509 -in "$1" -outform der | /usr/bin/shasum -a 1 | /usr/bin/awk '{print toupper($1)}'
}
expected_sha1=$(fingerprint "$expected_cert")
imported_sha1=$(fingerprint "$imported_cert")
if [ "$expected_sha1" != "$imported_sha1" ]; then
  fail "imported certificate SHA-1 $imported_sha1 does not match $expected_cert ($expected_sha1)"
fi
rm -f "$imported_cert"

identity=$(/usr/bin/openssl x509 -in "$expected_cert" -noout -subject \
  | /usr/bin/tr '/' ',' | /usr/bin/sed -n 's/^subject=.*CN=\([^,]*\).*$/\1/p')
if [ -z "$identity" ]; then
  fail "cannot read the certificate common name from $expected_cert"
fi

log "Imported signing certificate: subject=CN=$identity SHA-1=$expected_sha1"
log "MACOS_SIGNING_IDENTITY=$identity"
log "MACOS_SIGNING_MODE=certificate"
log "MACOS_SIGNING_KEYCHAIN=$keychain"
emit_env MACOS_SIGNING_IDENTITY "$identity"
emit_env MACOS_SIGNING_MODE certificate
emit_env MACOS_SIGNING_KEYCHAIN "$keychain"
