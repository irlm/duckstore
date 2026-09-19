#!/usr/bin/env bash
#
# test-bench-verify.sh — unit tests for duckstore-bench-verify.sh (no engines, no network)
#
set -uo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TARGET="${TEST_DIR}/../duckstore-bench-verify.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
RESULTS="${TMP}/results"
: > "$RESULTS"

assert_eq()    { if [ "$2" = "$3" ]; then echo "PASS: $1" >> "$RESULTS"; else echo "FAIL: $1 (want '$3', got '$2')" >> "$RESULTS"; fi; }
assert_match() { if printf '%s' "$2" | grep -Eq "$3"; then echo "PASS: $1" >> "$RESULTS"; else echo "FAIL: $1 (no match /$3/)" >> "$RESULTS"; fi; }
assert_rc()    { if [ "$2" -eq "$3" ]; then echo "PASS: $1" >> "$RESULTS"; else echo "FAIL: $1 (want rc $3, got $2)" >> "$RESULTS"; fi; }

bash "$TARGET" --help >/dev/null 2>&1; assert_rc "--help exits 0" $? 0
bash "$TARGET" --nonsense >/dev/null 2>&1; assert_rc "unknown flag exits 1" $? 1
bash "$TARGET" >/dev/null 2>&1; assert_rc "no engine exits 1" $? 1
bash "$TARGET" --engine postgres --reference postgres >/dev/null 2>&1; assert_rc "engine equal to reference exits 1" $? 1

# shellcheck disable=SC1090  # the target is a variable by design
source_run() { source "$TARGET" || true; set +e; }

(
  source_run
  # Postgres prints an empty field for NULL, sqlcmd prints the word; decimals and timestamp
  # formats differ too. After normalising, the same answer must look the same.
  pg=$'Toys\t1234.50\t\t2026-01-02 00:00:00\ttrue'
  ms=$'Toys  \t1234.4999\tNULL\t2026-01-02 00:00:00.000000\t1'
  assert_eq "normalize makes two engines comparable" \
    "$(printf '%s\n' "$pg" | normalize 2)" "$(printf '%s\n' "$ms" | normalize 2)"

  assert_eq "normalize keeps a real difference" \
    "$(printf 'a\t1.00\n' | normalize 2)" "$(printf 'a\t1.00\n' | normalize 2)"
  if [ "$(printf 'a\t1.00\n' | normalize 2)" = "$(printf 'a\t1.01\n' | normalize 2)" ]; then
    echo "FAIL: normalize must not hide a difference" >> "$RESULTS"
  else
    echo "PASS: normalize must not hide a difference" >> "$RESULTS"
  fi

  # sqlcmd prints .96 and pads zeros where psql prints 0.96
  assert_eq "normalize reads a number without its leading zero" \
    "$(printf 'x\t.960000000000\n' | normalize 2)" "$(printf 'x\t0.96\n' | normalize 2)"

  assert_eq "pg_duckdb reads the Postgres SQL folder" "$(dialect_dir pgduckdb)" "postgres"
  assert_eq "mssql has its own folder" "$(dialect_dir mssql)" "mssql"

  mkdir -p "$TMP/sql"
  : > "$TMP/sql/b.sql"; : > "$TMP/sql/a.sql"
  assert_eq "question_ids sorts the folder" "$(question_ids "$TMP/sql" | tr '\n' ' ')" "a b "
  assert_eq "question_ids keeps the asked order" "$(question_ids "$TMP/sql" b,a | tr '\n' ' ')" "b a "
) 2>/dev/null

src="$(cat "$TARGET")"
assert_match "source-guarded" "$src" '\[\[ "\$\{BASH_SOURCE\[0\]\}" == "\$0" \]\] && main'
assert_eq "no password on any command line" "$(printf '%s' "$src" | grep -cE '(sqlcmd|psql)[^|]*-(P|-password)[= ]')" "0"

grep -c '^PASS' "$RESULTS" | xargs -I{} echo "{} passed"
if grep -q '^FAIL' "$RESULTS"; then grep '^FAIL' "$RESULTS"; exit 1; fi
exit 0
