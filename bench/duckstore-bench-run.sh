#!/usr/bin/env bash
#
# duckstore-bench-run.sh — time the duckstore questions on one engine, warm or cold
#
# The Compare page measures what an application sees: a connection, a query, rows read
# and JSON written. This script measures the engine alone, the way NetworkLab's
# duckbench does, so results from both labs can sit in the same table: every engine is
# asked for its own timing (DuckDB .timer, psql \timing, sqlcmd SET STATISTICS TIME),
# all repetitions of a question run inside one process, and the first runs are warm-up.
#
# The SQL comes from the exporter, so a benchmark never runs SQL that differs from the
# application's:
#   docker compose run --rm --no-deps analytics export-sql --out /data/bench-sql
#
#   ./duckstore-bench-run.sh --engine duckdb   --sql-dir ./sql --duckdb-file /data/warehouse.duckdb
#   ./duckstore-bench-run.sh --engine postgres --sql-dir ./sql --model store --repeat 5
#   ./duckstore-bench-run.sh --engine postgres --sql-dir ./sql --cold --repeat 2
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/common.sh
. "${SCRIPT_DIR}/lib/common.sh"

ENGINE=""
MODELS="store,star"
SQL_DIR="${BENCH_SQL_DIR:-${SCRIPT_DIR}/sql}"
QUESTIONS=""
SCALE="${BENCH_SCALE:-5}"
REPEAT=3
WARMUP=1
COLD=0
COLD_OS=1
TIMEOUT="${BENCH_TIMEOUT:-1800}"
OUT_FILE=""
DRY_RUN=0

HOST="${BENCH_PG_HOST:-127.0.0.1}"
PORT="${BENCH_PG_PORT:-55432}"
DB="${BENCH_PG_DB:-store}"
USER_NAME="${BENCH_PG_USER:-store}"
MSSQL_HOST="${BENCH_MSSQL_HOST:-}"
MSSQL_PORT="${BENCH_MSSQL_PORT:-1433}"
MSSQL_DB="${BENCH_MSSQL_DB:-duckstore}"
MSSQL_USER="${BENCH_MSSQL_USER:-duckstore}"
DUCKDB_FILE="${BENCH_DUCKDB_FILE:-/data/warehouse.duckdb}"
DUCKDB_THREADS="${BENCH_DUCKDB_THREADS:-}"
DUCKDB_MEMORY_MB="${BENCH_DUCKDB_MEMORY_MB:-}"
PG_CONTAINER="${BENCH_PG_CONTAINER:-duckstore-postgres}"
# The clients. Each may be more than one word, so the same script works with a CLI on the
# PATH or one inside a container, e.g. BENCH_DUCKDB_CMD="docker run --rm -i -v duckstore_warehouse:/data duckstore-duckdb".
DUCKDB_CMD="${BENCH_DUCKDB_CMD:-duckdb}"
PSQL_CMD="${BENCH_PSQL_CMD:-psql}"
SQLCMD_CMD="${BENCH_SQLCMD:-sqlcmd}"

usage() { cat <<EOF
Usage: $0 --engine postgres|pgduckdb|duckdb|mssql [options]

  --engine E            engine to time
  --model store|star    data model, or both (default: ${MODELS})
  --sql-dir DIR         exported SQL root: DIR/<engine>/<model>/<question>.sql (default: ${SQL_DIR})
  --questions a,b       question ids (default: every file in the model folder)
  --scale N             scale label written to the results (default: ${SCALE})
  --repeat N            timed runs per question (default: ${REPEAT})
  --warmup N            runs discarded first (default: ${WARMUP})
  --cold                empty every cache before each timed run: the engine's own and the
                        operating system's page cache (the latter needs passwordless sudo)
  --cold-engine         empty only the engine's own cache (Postgres restart, SQL Server DBCC,
                        a fresh DuckDB process). The OS page cache stays warm, so this measures
                        the engine's buffer pool, not the disk. Say so when reporting it.
  --timeout N           give up on a question after N seconds (default: ${TIMEOUT}; 0 = no limit)
  --out FILE            append the TSV rows here (default: \$HOME/results-duckstore-<engine>-<stamp>.tsv)
  --host H --port N --db D --user U     Postgres/pg_duckdb connection (default: ${HOST}:${PORT}/${DB})
  --mssql-host H --mssql-port N --mssql-db D --mssql-user U
  --duckdb-file PATH    warehouse file for the duckdb engine (default: ${DUCKDB_FILE})
  --duckdb-threads N    threads for duckdb / pg_duckdb
  --duckdb-memory-mb N  memory limit for duckdb / pg_duckdb
  --container NAME      Postgres container restarted by --cold (default: ${PG_CONTAINER})
  --dry-run             print what would run
  -h, --help

Passwords come from the environment or /etc/lab-secrets.env, never from a flag:
PGPASSWORD (postgres, pgduckdb), SQLCMDPASSWORD (mssql), LAB_SQL_SA_PASSWORD (mssql --cold).
EOF
}

# ── pure helpers (unit-tested) ────────────────────────────────

# dialect_for ENGINE — the folder the exported SQL is in. pg_duckdb speaks Postgres.
dialect_for() {
  case "${1:-}" in
    duckdb)   printf 'duckdb' ;;
    postgres) printf 'postgres' ;;
    pgduckdb) printf 'postgres' ;;
    mssql)    printf 'mssql' ;;
    *) return 1 ;;
  esac
}

# parse_ms ENGINE < output — one millisecond value per timed statement.
# DuckDB prints "Run Time (s): real 0.123 ...", psql "Time: 123.456 ms", and
# sqlcmd "SQL Server Execution Times: ... elapsed time = 123 ms".
parse_ms() {
  case "${1:-}" in
    duckdb)
      awk '/Run Time \(s\):/ { for (i = 1; i <= NF; i++) if ($i == "real") printf "%.3f\n", $(i+1) * 1000 }'
      ;;
    postgres|pgduckdb)
      awk '/^Time: / { print $2 }'
      ;;
    mssql)
      # The parse-and-compile block comes first and is reported separately; only the
      # execution one matters. sub(), not match(..., arr): Ubuntu ships mawk.
      awk '/SQL Server Execution Times/ {
             if ((getline line) > 0 && sub(/.*elapsed time = /, "", line)) {
               sub(/ *ms.*/, "", line); print line
             }
           }'
      ;;
    *) return 1 ;;
  esac
}

# median LIST — median of the numbers given, to 1 decimal.
median() {
  printf '%s\n' "$@" | sort -n | awk '
    { v[NR] = $1 }
    END {
      if (NR == 0) { print "0.0"; exit }
      if (NR % 2) printf "%.1f\n", v[(NR + 1) / 2]
      else        printf "%.1f\n", (v[NR/2] + v[NR/2 + 1]) / 2
    }'
}

# query_files DIR LIST — the .sql files to run, in order.
query_files() {
  local dir="$1" list="${2:-}" q
  if [ -z "$list" ]; then
    find "$dir" -maxdepth 1 -name '*.sql' -type f 2>/dev/null | sort
    return 0
  fi
  for q in ${list//,/ }; do
    printf '%s/%s.sql\n' "$dir" "${q%.sql}"
  done
}

# label QUESTION_FILE MODEL — how the question appears in the results: id.model
label() { printf '%s.%s' "$(basename "${1%.sql}")" "$2"; }

# query_text FILE — the question, ending in exactly one semicolon. Without it the
# repetitions run as one statement and the engine reports a single time.
query_text() {
  local sql; sql="$(cat "$1")"
  sql="${sql%"${sql##*[![:space:]]}"}"
  printf '%s;\n' "${sql%;}"
}

# ── engine drivers ────────────────────────────────────────────
# Each prints the engine's raw output; parse_ms turns it into numbers.

script_duckdb() {
  local file="$1" reps="$2" i
  [ -n "$DUCKDB_THREADS" ]   && echo "SET threads=${DUCKDB_THREADS};"
  [ -n "$DUCKDB_MEMORY_MB" ] && echo "SET memory_limit='${DUCKDB_MEMORY_MB}MB';"
  # The warehouse is written in UTC; the Compare page pins the same, so dates match.
  echo "SET TimeZone='UTC';"
  echo ".timer on"
  for ((i = 0; i < reps; i++)); do query_text "$file"; done
  return 0
}

script_postgres() {
  local file="$1" reps="$2" i
  if [ "$ENGINE" = "pgduckdb" ]; then
    # Without this pg_duckdb hands the query back to the Postgres executor and the two
    # engines report identical times — a measurement that looks valid and is not.
    echo "SET duckdb.force_execution = true;"
    [ -n "$DUCKDB_THREADS" ]   && echo "SET duckdb.max_workers_per_postgres_scan = ${DUCKDB_THREADS};"
    [ -n "$DUCKDB_MEMORY_MB" ] && echo "SET duckdb.memory_limit = ${DUCKDB_MEMORY_MB};"
  fi
  echo '\timing on'
  for ((i = 0; i < reps; i++)); do query_text "$file"; done
  return 0
}

script_mssql() {
  local file="$1" reps="$2" i
  echo "SET STATISTICS TIME ON;"
  echo "GO"
  for ((i = 0; i < reps; i++)); do query_text "$file"; echo "GO"; done
}

# drop_os_cache — needs passwordless sudo; returns non-zero when it cannot. With --cold-engine
# it is skipped on purpose.
drop_os_cache() {
  [ "${COLD_OS:-1}" -eq 1 ] || return 0
  sync
  sudo -n sh -c 'echo 3 > /proc/sys/vm/drop_caches' 2>/dev/null
}

# docker_run ARGS... — docker without sudo when the user is in the docker group, with sudo -n
# otherwise. Restarting a container does not need root when the socket is readable.
docker_run() {
  if docker info >/dev/null 2>&1; then docker "$@"; else sudo -n docker "$@"; fi
}

# cold_prepare — empty every cache this engine reads through. A run that cannot do
# it is refused, never reported as if it were cold.
cold_prepare() {
  case "$ENGINE" in
    duckdb)
      drop_os_cache || return 1
      ;;
    postgres|pgduckdb)
      # Dropping the OS cache is not enough: shared_buffers keeps the data one layer up.
      docker_run restart "$PG_CONTAINER" >/dev/null 2>&1 || return 1
      local i
      for ((i = 0; i < 60; i++)); do
        docker_run exec "$PG_CONTAINER" pg_isready -q >/dev/null 2>&1 && break
        sleep 1
      done
      drop_os_cache || return 1
      ;;
    mssql)
      # DBCC DROPCLEANBUFFERS is sysadmin only. When the benchmark already connects as sa there is
      # nothing more to ask for; otherwise the sa password has to come from the environment.
      local client sa_password
      read -ra client <<< "$SQLCMD_CMD"
      if [ "$MSSQL_USER" = "sa" ]; then
        sa_password="$SQLCMDPASSWORD"
      else
        read_secret LAB_SQL_SA_PASSWORD "SQL Server sa password"
        sa_password="$LAB_SQL_SA_PASSWORD"
      fi
      SQLCMDPASSWORD="$sa_password" "${client[@]}" -S "${MSSQL_HOST},${MSSQL_PORT}" -U sa -d "$MSSQL_DB" -C -b \
        -Q "CHECKPOINT; DBCC DROPCLEANBUFFERS WITH NO_INFOMSGS; DBCC FREEPROCCACHE WITH NO_INFOMSGS;" >/dev/null 2>&1 || return 1
      ;;
    *) return 1 ;;
  esac
  return 0
}

# execute FILE REPS — run the question REPS times in one process, print the raw output.
execute() {
  local file="$1" reps="$2" client
  local limit=()
  [ "${TIMEOUT:-0}" -gt 0 ] && limit=(timeout "${TIMEOUT}s")
  case "$ENGINE" in
    duckdb)
      read -ra client <<< "$DUCKDB_CMD"
      script_duckdb "$file" "$reps" | "${limit[@]}" "${client[@]}" -readonly "$DUCKDB_FILE" 2>&1
      ;;
    postgres|pgduckdb)
      read -ra client <<< "$PSQL_CMD"
      script_postgres "$file" "$reps" | "${limit[@]}" "${client[@]}" -h "$HOST" -p "$PORT" -U "$USER_NAME" -d "$DB" -q -A -t 2>&1
      ;;
    mssql)
      read -ra client <<< "$SQLCMD_CMD"
      script_mssql "$file" "$reps" | "${limit[@]}" "${client[@]}" -S "${MSSQL_HOST},${MSSQL_PORT}" -U "$MSSQL_USER" -d "$MSSQL_DB" -C -h -1 2>&1
      ;;
  esac
}

emit() { # emit QUERY REP MS
  local line
  line="$(printf '%s\t%s\t%s\t%s\t%s\t%s\t%s' "$(iso_now)" "$(hostname)" "$ENGINE" "$SCALE" "$1" "$2" "$3")"
  printf '%s\n' "$line"
  [ -n "$OUT_FILE" ] && printf '%s\n' "$line" >> "$OUT_FILE"
  return 0
}

# run_question FILE MODEL — time one question and emit its rows.
run_question() {
  local file="$1" model="$2" name times=() ms rep=0
  name="$(label "$file" "$model")"

  if [ "$COLD" -eq 1 ]; then
    # One process per timed run: the caches are emptied in between, so nothing may
    # survive inside the client either.
    local i
    for ((i = 0; i < REPEAT; i++)); do
      cold_prepare || die "${name}: could not empty the caches — refusing to report a warm run as cold."
      mapfile -t ms < <(execute "$file" 1 | parse_ms "$ENGINE")
      if [ "${#ms[@]}" -lt 1 ]; then
        warn "${name}: no answer within ${TIMEOUT}s (or the engine printed no timing) — skipped"
        return 0
      fi
      times+=("${ms[0]}")
    done
  else
    mapfile -t times < <(execute "$file" "$((WARMUP + REPEAT))" | parse_ms "$ENGINE")
    if [ "${#times[@]}" -eq 0 ]; then
      warn "${name}: no answer within ${TIMEOUT}s (or the engine printed no timing) — skipped"
      return 0
    fi
    if [ "${#times[@]}" -ne "$((WARMUP + REPEAT))" ]; then
      warn "${name}: expected $((WARMUP + REPEAT)) timings, got ${#times[@]} — check the question or the engine output"
    fi
    times=("${times[@]:$WARMUP}")
  fi

  for ms in "${times[@]}"; do
    rep=$((rep + 1))
    emit "$name" "$rep" "$ms"
  done
  log "$(printf '%-34s median %10s ms  (%s runs)' "$name" "$(median "${times[@]}")" "${#times[@]}")"
}

main() {
  while [ $# -gt 0 ]; do
    case "$1" in
      --engine) ENGINE="${2:-}"; shift 2 ;;
      --model) MODELS="${2:-}"; shift 2 ;;
      --sql-dir) SQL_DIR="${2:-}"; shift 2 ;;
      --questions) QUESTIONS="${2:-}"; shift 2 ;;
      --scale) SCALE="${2:-}"; shift 2 ;;
      --repeat) REPEAT="${2:-}"; shift 2 ;;
      --warmup) WARMUP="${2:-}"; shift 2 ;;
      --cold) COLD=1; COLD_OS=1; shift ;;
      --cold-engine) COLD=1; COLD_OS=0; shift ;;
      --timeout) TIMEOUT="${2:-}"; shift 2 ;;
      --out) OUT_FILE="${2:-}"; shift 2 ;;
      --host) HOST="${2:-}"; shift 2 ;;
      --port) PORT="${2:-}"; shift 2 ;;
      --db) DB="${2:-}"; shift 2 ;;
      --user) USER_NAME="${2:-}"; shift 2 ;;
      --mssql-host) MSSQL_HOST="${2:-}"; shift 2 ;;
      --mssql-port) MSSQL_PORT="${2:-}"; shift 2 ;;
      --mssql-db) MSSQL_DB="${2:-}"; shift 2 ;;
      --mssql-user) MSSQL_USER="${2:-}"; shift 2 ;;
      --duckdb-file) DUCKDB_FILE="${2:-}"; shift 2 ;;
      --duckdb-threads) DUCKDB_THREADS="${2:-}"; shift 2 ;;
      --duckdb-memory-mb) DUCKDB_MEMORY_MB="${2:-}"; shift 2 ;;
      --container) PG_CONTAINER="${2:-}"; shift 2 ;;
      --dry-run) DRY_RUN=1; shift ;;
      -h|--help) usage; return 0 ;;
      *) usage; die "Unknown argument: $1" ;;
    esac
  done

  local dialect
  dialect="$(dialect_for "$ENGINE")" || { usage; die "--engine must be postgres, pgduckdb, duckdb or mssql (got '${ENGINE}')."; }
  [ -d "$SQL_DIR/$dialect" ] || die "No exported SQL in ${SQL_DIR}/${dialect} — run export-sql first (see the header of this script)."

  case "$ENGINE" in
    duckdb) require_cmd "${DUCKDB_CMD%% *}" "install the DuckDB CLI, or set BENCH_DUCKDB_CMD" ;;
    postgres|pgduckdb) require_cmd "${PSQL_CMD%% *}" "apt install postgresql-client-18, or set BENCH_PSQL_CMD"; read_secret PGPASSWORD "Postgres password for ${USER_NAME}"; export PGPASSWORD ;;
    mssql) require_cmd "${SQLCMD_CMD%% *}" "install go-sqlcmd, or set BENCH_SQLCMD"; [ -n "$MSSQL_HOST" ] || die "--mssql-host is required."
           read_secret SQLCMDPASSWORD "SQL Server password for ${MSSQL_USER}"; export SQLCMDPASSWORD ;;
  esac

  [ -n "$OUT_FILE" ] || OUT_FILE="$(default_out "$ENGINE")"

  if [ "$DRY_RUN" -eq 1 ]; then
    log "[DRY-RUN] engine=${ENGINE} models=${MODELS} sql=${SQL_DIR}/${dialect} repeat=${REPEAT} warmup=${WARMUP} cold=${COLD} out=${OUT_FILE}"
    return 0
  fi

  local model file count=0
  for model in ${MODELS//,/ }; do
    local dir="${SQL_DIR}/${dialect}/${model}"
    [ -d "$dir" ] || { warn "no ${model} SQL for ${ENGINE} in ${dir}"; continue; }
    hdr "${ENGINE}, ${model} model$([ "$COLD" -eq 1 ] && { [ "$COLD_OS" -eq 1 ] && echo ', cold cache' || echo ', cold engine cache (OS cache warm)'; })"
    while read -r file; do
      [ -f "$file" ] || { warn "missing $file"; continue; }
      run_question "$file" "$model"
      count=$((count + 1))
    done < <(query_files "$dir" "$QUESTIONS")
  done

  [ "$count" -gt 0 ] || die "No questions ran."
  ok "${count} questions, results appended to ${OUT_FILE}"
}

[[ "${BASH_SOURCE[0]}" == "$0" ]] && main "$@"
