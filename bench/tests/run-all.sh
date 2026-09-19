#!/usr/bin/env bash
#
# run-all.sh — syntax, shellcheck and every unit test in this folder
#
# No root, no network, no databases. Run it before committing:
#   bash bench/tests/run-all.sh
#
set -uo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BENCH_DIR="$(cd "${TEST_DIR}/.." && pwd)"
SHELLCHECK="${SHELLCHECK:-shellcheck}"
failed=0

echo "== syntax =="
while read -r f; do
  bash -n "$f" || { echo "FAIL: bash -n $f"; failed=1; }
done < <(find "$BENCH_DIR" -name '*.sh' -type f | sort)
echo "ok"

echo "== shellcheck =="
if command -v "$SHELLCHECK" >/dev/null 2>&1; then
  while read -r f; do
    "$SHELLCHECK" -S warning -x "$f" || failed=1
  done < <(find "$BENCH_DIR" -name '*.sh' -type f | sort)
  echo "ok"
else
  echo "skipped (shellcheck not installed)"
fi

echo "== no default passwords in the scripts =="
if grep -rnE '(PASSWORD|PASS|PASSWD)=["'"'"']?(P@ssw0rd|changeme|password|admin)' "$BENCH_DIR" --include='*.sh'; then
  echo "FAIL: a default password appears in a script"
  failed=1
else
  echo "ok"
fi

echo "== unit tests =="
for t in "$TEST_DIR"/test-*.sh; do
  [ -f "$t" ] || continue
  echo "-- $(basename "$t")"
  bash "$t" || failed=1
done

[ "$failed" -eq 0 ] && echo "ALL OK" || echo "FAILURES"
exit "$failed"
