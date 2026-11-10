#!/bin/bash
# Remove the temporary release-signing keychain and restore the user's
# keychain search list. Safe to run when setup never ran: it only removes
# files under RUNNER_TEMP (or TMPDIR) and never touches login/default
# keychains.

set -euo pipefail

keychain=${MACOS_SIGNING_KEYCHAIN:-${RUNNER_TEMP:-${TMPDIR:-/tmp}}/anypic-signing.keychain-db}
keychain=${keychain%/}
state_file="$keychain.search-list"
keychain_password_file="$keychain.password"

if [ -f "$state_file" ]; then
  search_args=()
  while IFS= read -r line; do
    line=${line#"${line%%[![:space:]]*}"}
    line=${line%\"}
    line=${line#\"}
    if [ -n "$line" ]; then
      search_args+=("$line")
    fi
  done < "$state_file"
  if [ "${#search_args[@]}" -gt 0 ]; then
    security list-keychains -d user -s "${search_args[@]}"
  fi
fi

security delete-keychain "$keychain" 2>/dev/null || true
rm -f "$keychain_password_file" "$state_file" "$keychain.p12" "$keychain.imported.pem"
