#!/usr/bin/env bash
# Rename this template into a new service.
#   ./scripts/rename.sh github.com/acme/billing billing
# Rewrites the Go module path and the service name in every tracked text
# file, then tidies modules. Run it once, right after creating your repo.
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 NEW_MODULE NEW_NAME" >&2
  exit 2
fi
new_module=$1
new_name=$2
old_module=github.com/ShammiAnand/go-service-template
old_name=go-service-template

if [[ ! $new_module =~ ^[A-Za-z0-9._~/-]+$ ]]; then
  echo "invalid module path: $new_module" >&2
  exit 2
fi
if [[ ! $new_name =~ ^[a-z0-9][a-z0-9-]*$ ]]; then
  echo "invalid name (lowercase letters, digits, dashes): $new_name" >&2
  exit 2
fi

cd "$(git rev-parse --show-toplevel)"

files=$(git ls-files | grep -vE '^(LICENSE|scripts/rename\.sh)$' | xargs grep -IlE "$old_module|$old_name" || true)
for f in $files; do
  OLD_MODULE=$old_module NEW_MODULE=$new_module OLD_NAME=$old_name NEW_NAME=$new_name \
    perl -pi -e 's/\Q$ENV{OLD_MODULE}\E/$ENV{NEW_MODULE}/g; s/\Q$ENV{OLD_NAME}\E/$ENV{NEW_NAME}/g' "$f"
done

# The rename script, its make target and its CI job only make sense in the
# template itself.
perl -0pi -e 's/\n  rename:\n.*?(?=\n  docker:)//s' .github/workflows/ci.yml
perl -0pi -e 's/## rename:.*?\n\n//s; s/ rename build/ build/' Makefile
git rm -q --cached scripts/rename.sh 2>/dev/null || true
rm -f scripts/rename.sh

go mod tidy
(cd tools && go mod tidy)
echo "renamed to $new_module ($new_name). Review with: git diff --stat"
