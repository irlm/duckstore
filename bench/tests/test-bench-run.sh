#!/usr/bin/env bash
#
# test-bench-run.sh — unit tests for duckstore-bench-run.sh
#
# No root, no network, no engines: the script is sourced (its source guard keeps main
# from running) and its pure helpers are called directly, with real client output
# captured as heredocs.
#
set -uo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TARGET="${TEST_DIR}/../duckstore-bench-run.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
RESULTS="${TMP}/results"
: > "$RESULTS"

assert_eq()    { if [ "$2" = "$3" ]; then echo "PASS: $1" >> "$RESULTS"; else echo "FAIL: $1 (want '$3', got '$2')" >> "$RESULTS"; fi; }
assert_match() { if printf '%s' "$2" | grep -Eq "$3"; then echo "PASS: $1" >> "$RESULTS"; else echo "FAIL: $1 (no match /$3/ in '$2')" >> "$RESULTS"; fi; }
assert_rc()    { if [ "$2" -eq "$3" ]; then echo "PASS: $1" >> "$RESULTS"; else echo "FAIL: $1 (want rc $3, got $2)" >> "$RESULTS"; fi; }

# ── argument handling (the real script, as a subprocess) ──────
bash "$TARGET" --help >/dev/null 2>&1; assert_rc "--help exits 0" $? 0
bash "$TARGET" --nonsense >/dev/null 2>&1; assert_rc "unknown flag exits 1" $? 1
bash "$TARGET" --engine oracle >/dev/null 2>&1; assert_rc "unknown engine exits 1" $? 1
bash "$TARGET" --engine duckdb --sql-dir "$TMP/none" >/dev/null 2>&1; assert_rc "missing sql dir exits 1" $? 1

# ── pure helpers (sourced) ────────────────────────────────────
# shellcheck disable=SC1090  # the target is a variable by design
source_run() { source "$TARGET" || true; set +e; }

(
  source_run
  assert_eq "dialect_for duckdb"   "$(dialect_for duckdb)"   "duckdb"
  assert_eq "dialect_for postgres" "$(dialect_for postgres)" "postgres"
  assert_eq "pg_duckdb runs the Postgres SQL" "$(dialect_for pgduckdb)" "postgres"
  assert_eq "dialect_for mssql"    "$(dialect_for mssql)"    "mssql"
  dialect_for redis >/dev/null 2>&1; assert_rc "dialect_for rejects an unknown engine" $? 1

  assert_eq "median, odd count"  "$(median 10 30 20)"     "20.0"
  assert_eq "median, even count" "$(median 10 20 30 40)"  "25.0"
  assert_eq "median of nothing"  "$(median)"              "0.0"

  assert_eq "label adds the model" "$(label /x/y/monthly-revenue.sql star)" "monthly-revenue.star"

  printf 'SELECT 1\n' > "$TMP/no-semicolon.sql"
  printf 'SELECT 1;\n\n' > "$TMP/with-semicolon.sql"
  assert_eq "query_text ends every question with one semicolon" "$(query_text "$TMP/no-semicolon.sql")" "SELECT 1;"
  assert_eq "query_text does not double the semicolon" "$(query_text "$TMP/with-semicolon.sql")" "SELECT 1;"
) 2>/dev/null

(
  source_run
  duckdb_out=$(cat <<'EOF'
Run Time (s): real 0.300 user 2.100000 sys 0.080000
Run Time (s): real 1.085 user 6.000000 sys 0.200000
EOF
)
  assert_eq "parse_ms duckdb" "$(printf '%s\n' "$duckdb_out" | parse_ms duckdb | tr '\n' ' ')" "300.000 1085.000 "

  psql_out=$(cat <<'EOF'
Time: 9813.402 ms (00:09.813)
Time: 1238.100 ms (00:01.238)
EOF
)
  assert_eq "parse_ms postgres" "$(printf '%s\n' "$psql_out" | parse_ms postgres | tr '\n' ' ')" "9813.402 1238.100 "
  assert_eq "parse_ms pgduckdb" "$(printf '%s\n' "$psql_out" | parse_ms pgduckdb | tr '\n' ' ')" "9813.402 1238.100 "

  # sqlcmd prints parse-and-compile first; only the execution block counts.
  mssql_out=$(cat <<'EOF'
SQL Server parse and compile time:
   CPU time = 15 ms, elapsed time = 20 ms.

 SQL Server Execution Times:
   CPU time = 16000 ms,  elapsed time = 4143 ms.
EOF
)
  assert_eq "parse_ms mssql takes the execution time" "$(printf '%s\n' "$mssql_out" | parse_ms mssql | tr -d ' \n')" "4143"

  parse_ms redis >/dev/null 2>&1; assert_rc "parse_ms rejects an unknown engine" $? 1
) 2>/dev/null

(
  source_run
  mkdir -p "$TMP/sql/postgres/store"
  : > "$TMP/sql/postgres/store/monthly-revenue.sql"
  : > "$TMP/sql/postgres/store/active-customers.sql"
  assert_eq "query_files sorts the folder" \
    "$(query_files "$TMP/sql/postgres/store" | xargs -n1 basename | tr '\n' ' ')" \
    "active-customers.sql monthly-revenue.sql "
  assert_eq "query_files keeps the asked order" \
    "$(query_files "$TMP/sql/postgres/store" monthly-revenue,active-customers | xargs -n1 basename | tr '\n' ' ')" \
    "monthly-revenue.sql active-customers.sql "
) 2>/dev/null

# ── static checks ─────────────────────────────────────────────
src="$(cat "$TARGET")"
assert_match "source-guarded, so tests can source it" "$src" '\[\[ "\$\{BASH_SOURCE\[0\]\}" == "\$0" \]\] && main'
assert_match "opens duckdb read-only" "$src" '\$\{client\[@\]\}" -readonly'
assert_match "refuses a cold run it could not make cold" "$src" 'refusing to report a warm run as cold'
assert_match "gives up on a question that never answers" "$src" 'timeout "\$\{TIMEOUT\}s"'
assert_match "the question loop keeps its stdin" "$src" 'run_question "\$file" "\$model" < /dev/null'
assert_eq "no password on any command line" "$(printf '%s' "$src" | grep -cE '(sqlcmd|psql)[^|]*-(P|-password)[= ]')" "0"

# ── report ────────────────────────────────────────────────────
grep -c '^PASS' "$RESULTS" | xargs -I{} echo "{} passed"
if grep -q '^FAIL' "$RESULTS"; then grep '^FAIL' "$RESULTS"; exit 1; fi
exit 0
