#!/usr/bin/env bash
#
# duckstore-bench-verify.sh — do the engines give the same answer?
#
# A benchmark that has not proved this measures nothing in particular. Each question is run
# on a reference engine and on another one, both results are normalised (spaces, NULL words,
# decimals, timestamp formats) and compared line by line.
#
#   ./duckstore-bench-verify.sh --engine duckdb                  against Postgres, every question
#   ./duckstore-bench-verify.sh --engine mssql --model star
#   ./duckstore-bench-verify.sh --engine mssql --questions monthly-revenue --show 20
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/common.sh
. "${SCRIPT_DIR}/lib/common.sh"

REFERENCE="postgres"
ENGINE=""
MODELS="store,star"
QUESTIONS=""
SQL_DIR="${BENCH_SQL_DIR:-${SCRIPT_DIR}/sql}"
DECIMALS=2
SHOW=6
TIMEOUT="${BENCH_TIMEOUT:-1800}"
SORTED=0

HOST="${BENCH_PG_HOST:-127.0.0.1}"
PORT="${BENCH_PG_PORT:-55432}"
DB="${BENCH_PG_DB:-store}"
USER_NAME="${BENCH_PG_USER:-store}"
MSSQL_HOST="${BENCH_MSSQL_HOST:-127.0.0.1}"
MSSQL_PORT="${BENCH_MSSQL_PORT:-51433}"
MSSQL_DB="${BENCH_MSSQL_DB:-duckstore}"
MSSQL_USER="${BENCH_MSSQL_USER:-sa}"
DUCKDB_FILE="${BENCH_DUCKDB_FILE:-/data/warehouse.duckdb}"
DUCKDB_CMD="${BENCH_DUCKDB_CMD:-duckdb}"
PSQL_CMD="${BENCH_PSQL_CMD:-psql}"
SQLCMD_CMD="${BENCH_SQLCMD:-sqlcmd}"

usage() { cat <<EOF
Usage: $0 --engine postgres|pgduckdb|duckdb|mssql [options]

  --engine E            the engine to check
  --reference E         what to compare against (default: ${REFERENCE})
  --model store|star    data model, or both (default: ${MODELS})
  --questions a,b       question ids (default: every exported question)
  --sql-dir DIR         exported SQL root (default: ${SQL_DIR})
  --decimals N          decimals kept when comparing numbers (default: ${DECIMALS})
  --show N              differing lines to print (default: ${SHOW})
  --timeout N           give up on a question after N seconds (default: ${TIMEOUT}; 0 = no limit)
  --sorted              compare the rows sorted, ignoring row order
  -h, --help
EOF
}

# ── pure helpers (unit-tested) ────────────────────────────────

# normalize DECIMALS < rows — make two engines' output comparable: trim spaces, empty out the
# NULL word, round numbers, drop trailing zeros of a timestamp and a midnight time.
normalize() {
  awk -F'\t' -v d="${1:-2}" 'BEGIN { OFS = "\t" }
    {
      for (i = 1; i <= NF; i++) {
        v = $i
        gsub(/^[ \t\r]+|[ \t\r]+$/, "", v)
        if (v == "NULL" || v == "null") { v = "" }
        # sqlcmd prints .96 where psql prints 0.96, and pads trailing zeros, so a number is
        # matched with or without the leading digit and always reprinted with d decimals.
        else if (v ~ /^-?[0-9]*\.[0-9]+([eE][-+]?[0-9]+)?$/) { v = sprintf("%.*f", d, v + 0) }
        else if (v ~ /^-?[0-9]+$/) { v = v + 0 }
        else if (v ~ /^[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9][ T][0-9][0-9]:[0-9][0-9]:[0-9][0-9]/) {
          # psql prints a timestamptz as 2026-09-17 11:43:59.507746+00, sqlcmd the same
          # instant as 2026-09-17 11:43:59.5077460, DuckDB without the zone. Everything is
          # UTC here, so the zone and the fraction go.
          sub(/T/, " ", v)
          sub(/[+-][0-9][0-9](:?[0-9][0-9])?$/, "", v)
          sub(/Z$/, "", v)
          sub(/\.[0-9]+$/, "", v)
          sub(/ 00:00:00$/, "", v)
        }
        else if (v == "true" || v == "t") { v = "1" }
        else if (v == "false" || v == "f") { v = "0" }
        $i = v
      }
      print
    }'
}

# question_ids DIR LIST — ids to check, in order.
question_ids() {
  local dir="$1" list="${2:-}" q
  if [ -z "$list" ]; then
    find "$dir" -maxdepth 1 -name '*.sql' -type f 2>/dev/null | sed 's|.*/||; s|\.sql$||' | sort
    return 0
  fi
  for q in ${list//,/ }; do printf '%s\n' "${q%.sql}"; done
}

# ── engines ───────────────────────────────────────────────────

# rows ENGINE FILE — the result of one question as tab separated lines, no header.
rows() {
  local engine="$1" file="$2" client
  local limit=()
  [ "${TIMEOUT:-0}" -gt 0 ] && limit=(timeout "${TIMEOUT}s")
  case "$engine" in
    duckdb)
      read -ra client <<< "$DUCKDB_CMD"
      { echo "SET TimeZone='UTC';"; echo ".mode list"; echo ".separator \"\t\""; echo ".headers off"; cat "$file"; echo ";"; } \
        | "${limit[@]}" "${client[@]}" -readonly "$DUCKDB_FILE" 2>/dev/null
      ;;
    postgres|pgduckdb)
      read -ra client <<< "$PSQL_CMD"
      { [ "$engine" = "pgduckdb" ] && echo "SET duckdb.force_execution = true;"; cat "$file"; echo ";"; } \
        | "${limit[@]}" "${client[@]}" -h "$HOST" -p "$PORT" -U "$USER_NAME" -d "$DB" -q -A -t -F $'\t' 2>/dev/null
      ;;
    mssql)
      read -ra client <<< "$SQLCMD_CMD"
      { echo "SET NOCOUNT ON;"; echo "GO"; cat "$file"; echo ";"; echo "GO"; } \
        | "${limit[@]}" "${client[@]}" -S "${MSSQL_HOST},${MSSQL_PORT}" -U "$MSSQL_USER" -d "$MSSQL_DB" -C -h -1 -W -s $'\t' 2>/dev/null
      ;;
    *) die "unknown engine ${engine}" ;;
  esac
}

dialect_dir() {
  case "$1" in
    pgduckdb) printf 'postgres' ;;
    *) printf '%s' "$1" ;;
  esac
}

main() {
  while [ $# -gt 0 ]; do
    case "$1" in
      --engine) ENGINE="${2:-}"; shift 2 ;;
      --reference) REFERENCE="${2:-}"; shift 2 ;;
      --model|--models) MODELS="${2:-}"; shift 2 ;;
      --questions) QUESTIONS="${2:-}"; shift 2 ;;
      --sql-dir) SQL_DIR="${2:-}"; shift 2 ;;
      --decimals) DECIMALS="${2:-}"; shift 2 ;;
      --show) SHOW="${2:-}"; shift 2 ;;
      --timeout) TIMEOUT="${2:-}"; shift 2 ;;
      --sorted) SORTED=1; shift ;;
      --host) HOST="${2:-}"; shift 2 ;;
      --port) PORT="${2:-}"; shift 2 ;;
      --db) DB="${2:-}"; shift 2 ;;
      --user) USER_NAME="${2:-}"; shift 2 ;;
      --mssql-host) MSSQL_HOST="${2:-}"; shift 2 ;;
      --mssql-port) MSSQL_PORT="${2:-}"; shift 2 ;;
      --mssql-db) MSSQL_DB="${2:-}"; shift 2 ;;
      --mssql-user) MSSQL_USER="${2:-}"; shift 2 ;;
      --duckdb-file) DUCKDB_FILE="${2:-}"; shift 2 ;;
      -h|--help) usage; return 0 ;;
      *) usage; die "Unknown argument: $1" ;;
    esac
  done

  [ -n "$ENGINE" ] || { usage; die "--engine is required."; }
  [ "$ENGINE" != "$REFERENCE" ] || die "--engine and --reference are the same."

  # Both sides need their client and their password before anything runs: a missing client
  # would otherwise look like an empty result, which reads as "the answers differ".
  local side
  for side in "$REFERENCE" "$ENGINE"; do
    case "$side" in
      duckdb) require_cmd "${DUCKDB_CMD%% *}" "install the DuckDB CLI, or set BENCH_DUCKDB_CMD" ;;
      postgres|pgduckdb)
        require_cmd "${PSQL_CMD%% *}" "apt install postgresql-client-18, or set BENCH_PSQL_CMD"
        read_secret PGPASSWORD "Postgres password for ${USER_NAME}"; export PGPASSWORD ;;
      mssql)
        require_cmd "${SQLCMD_CMD%% *}" "install go-sqlcmd, or set BENCH_SQLCMD"
        read_secret SQLCMDPASSWORD "SQL Server password for ${MSSQL_USER}"; export SQLCMDPASSWORD ;;
      *) die "unknown engine ${side}" ;;
    esac
  done

  # Not a local: the EXIT trap runs after main returns, when a local is already gone.
  BENCH_TMP="$(mktemp -d)"
  trap 'rm -rf "${BENCH_TMP:-}"' EXIT

  local model id same=0 different=0 missing=0
  for model in ${MODELS//,/ }; do
    local ref_dir eng_dir
    ref_dir="${SQL_DIR}/$(dialect_dir "$REFERENCE")/${model}"
    eng_dir="${SQL_DIR}/$(dialect_dir "$ENGINE")/${model}"
    [ -d "$ref_dir" ] || { warn "no ${model} SQL for ${REFERENCE}"; continue; }
    hdr "${ENGINE} vs ${REFERENCE}, ${model} model"

    while read -r id; do
      [ -n "$id" ] || continue
      if [ ! -f "${eng_dir}/${id}.sql" ]; then
        warn "$(printf '%-34s no SQL for %s' "$id" "$ENGINE")"
        missing=$((missing + 1))
        continue
      fi

      rows "$REFERENCE" "${ref_dir}/${id}.sql" | normalize "$DECIMALS" > "${BENCH_TMP}/ref"
      rows "$ENGINE" "${eng_dir}/${id}.sql" | normalize "$DECIMALS" > "${BENCH_TMP}/eng"
      if [ "$SORTED" -eq 1 ]; then
        sort -o "${BENCH_TMP}/ref" "${BENCH_TMP}/ref"
        sort -o "${BENCH_TMP}/eng" "${BENCH_TMP}/eng"
      fi

      local ref_rows eng_rows
      ref_rows="$(wc -l < "${BENCH_TMP}/ref")"
      eng_rows="$(wc -l < "${BENCH_TMP}/eng")"

      # An empty side almost always means the client was killed by --timeout, not that the
      # engine answered nothing: say so instead of printing a diff of every row.
      if [ "$eng_rows" -eq 0 ] && [ "$ref_rows" -gt 0 ]; then
        err "$(printf '%-34s no answer from %s within %ss' "${id}.${model}" "$ENGINE" "$TIMEOUT")"
        different=$((different + 1))
        continue
      fi

      if cmp -s "${BENCH_TMP}/ref" "${BENCH_TMP}/eng"; then
        ok "$(printf '%-34s %s rows, same answer' "${id}.${model}" "$ref_rows")"
        same=$((same + 1))
      else
        err "$(printf '%-34s %s rows on %s, %s on %s' "${id}.${model}" "$ref_rows" "$REFERENCE" "$eng_rows" "$ENGINE")"
        diff -u "${BENCH_TMP}/ref" "${BENCH_TMP}/eng" | sed -n "1,$((SHOW + 3))p" >&2 || true
        different=$((different + 1))
      fi
    done < <(question_ids "$ref_dir" "$QUESTIONS")
  done

  echo >&2
  if [ "$different" -eq 0 ] && [ "$same" -gt 0 ]; then
    ok "${same} questions match${missing:+, ${missing} without SQL}"
    return 0
  fi
  err "${different} of $((same + different)) questions differ${missing:+, ${missing} without SQL}"
  return 1
}

[[ "${BASH_SOURCE[0]}" == "$0" ]] && main "$@"
