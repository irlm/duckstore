#!/usr/bin/env bash
#
# duckstore-bench-local.sh — one command: see what this machine can run, then run it
#
# Not every machine can run every engine. SQL Server has no ARM build, a Raspberry Pi has
# no room for three engines, a Mac runs DuckDB natively but Docker in a VM. So this script
# first checks what is available here, prints it, and runs only that.
#
#   ./duckstore-bench-local.sh --list             what this machine can run, and why not
#   ./duckstore-bench-local.sh                    pick from a menu, then run
#   ./duckstore-bench-local.sh --yes              run everything available, no questions
#   ./duckstore-bench-local.sh --engines duckdb,postgres --questions monthly-revenue --repeat 5
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/common.sh
. "${SCRIPT_DIR}/lib/common.sh"

ENGINES=""
QUESTIONS=""
MODELS="store,star"
REPEAT=3
WARMUP=1
SCALE="${BENCH_SCALE:-5}"
COLD=0
LIST_ONLY=0
ASSUME_YES=0
SKIP_EXPORT=0
OUT_FILE=""
export DRY_RUN=0
SQL_DIR="${BENCH_SQL_DIR:-${SCRIPT_DIR}/sql}"

COMPOSE=(docker compose)
PROJECT="${BENCH_COMPOSE_PROJECT:-duckstore}"

usage() { cat <<EOF
Usage: $0 [options]

  --list              print what this machine can run, and why the rest cannot
  --engines a,b       engines to run (default: every available one)
  --questions a,b     question ids (default: all)
  --model store|star  data model, or both (default: ${MODELS})
  --repeat N          timed runs per question (default: ${REPEAT})
  --warmup N          discarded runs (default: ${WARMUP})
  --scale N           scale label for the results (default: ${SCALE})
  --cold              empty caches before every timed run
  --skip-export       reuse the SQL already in ${SQL_DIR}
  --yes               do not ask, run everything available
  --out FILE          append results here (default: \$HOME/results-duckstore-local-<stamp>.tsv)
  --dry-run           print what would run
  -h, --help
EOF
}

# ── what this machine can do (pure where it can be) ───────────

ARCH="$(uname -m)"
OS="$(uname -s)"

# engine_supported_here ARCH OS ENGINE — hardware and OS limits, before anything is installed.
# SQL Server ships for x86_64 only: on a Pi or an Apple Silicon Mac it can only run
# elsewhere, on a lab machine.
engine_supported_here() {
  local arch="$1" engine="$3"
  case "$engine" in
    mssql) [ "$arch" = "x86_64" ] || [ "$arch" = "amd64" ] ;;
    *) return 0 ;;
  esac
}

have_docker() { docker info >/dev/null 2>&1; }

# Why docker refused, in the words of the fix. Being in the docker group is not enough:
# a login session that started before the group was added does not have it yet.
docker_reason() {
  cmd_exists docker || { printf 'docker is not installed'; return; }
  if id -nG 2>/dev/null | tr ' ' '\n' | grep -qx docker; then
    printf 'docker refused the connection (is the daemon running?)'
  elif getent group docker 2>/dev/null | grep -q "[:,]$(id -un)\b"; then
    printf 'you are in the docker group, but this login session is older than that: log out and back in (or run: newgrp docker)'
  else
    printf 'no permission for the docker socket: sudo usermod -aG docker %s, then log out and back in' "$(id -un)"
  fi
}

container_running() { [ "$(docker inspect -f '{{.State.Running}}' "$1" 2>/dev/null)" = "true" ]; }

# Reasons are filled in by detect(); ENGINE_STATE[engine] is "yes" or the reason it is not.
declare -A ENGINE_STATE=()
declare -A ENGINE_NOTE=()

detect() {
  local docker_ok=1
  have_docker || docker_ok=0

  # Postgres and pg_duckdb: the container, with psql either here or inside it.
  if [ "$docker_ok" -eq 1 ] && container_running "${PROJECT}-postgres"; then
    if cmd_exists psql; then
      BENCH_PSQL_CMD="psql"
      BENCH_PG_HOST="${BENCH_PG_HOST:-127.0.0.1}"; BENCH_PG_PORT="${BENCH_PG_PORT:-55432}"
    else
      BENCH_PSQL_CMD="docker exec -i -e PGPASSWORD ${PROJECT}-postgres psql"
      BENCH_PG_HOST="127.0.0.1"; BENCH_PG_PORT="5432"
    fi
    ENGINE_STATE[postgres]="yes"
    ENGINE_NOTE[postgres]="container ${PROJECT}-postgres"

    if PGPASSWORD="${BENCH_PG_PASSWORD:-store}" psql_query "SELECT extversion FROM pg_extension WHERE extname = 'pg_duckdb'" | grep -q .; then
      ENGINE_STATE[pgduckdb]="yes"
      ENGINE_NOTE[pgduckdb]="extension installed"
    else
      ENGINE_STATE[pgduckdb]="pg_duckdb is not installed in that database (make docker-pgduckdb)"
    fi
  elif [ "$docker_ok" -eq 0 ]; then
    local why; why="$(docker_reason)"
    ENGINE_STATE[postgres]="$why"
    ENGINE_STATE[pgduckdb]="$why"
  else
    ENGINE_STATE[postgres]="the container ${PROJECT}-postgres is not running (make docker-up)"
    ENGINE_STATE[pgduckdb]="the container ${PROJECT}-postgres is not running (make docker-up)"
  fi

  # DuckDB: the warehouse file, read by a CLI here or by one in a container.
  local duck_file="${BENCH_DUCKDB_FILE:-}"
  if [ -n "$duck_file" ] && [ -f "$duck_file" ] && cmd_exists duckdb; then
    BENCH_DUCKDB_CMD="duckdb"
    ENGINE_STATE[duckdb]="yes"
    ENGINE_NOTE[duckdb]="$duck_file"
  elif [ "$docker_ok" -eq 1 ] && docker image inspect duckstore-duckdb >/dev/null 2>&1; then
    BENCH_DUCKDB_CMD="docker run --rm -i -v ${PROJECT}_warehouse:/data duckstore-duckdb"
    BENCH_DUCKDB_FILE="/data/warehouse.duckdb"
    ENGINE_STATE[duckdb]="yes"
    ENGINE_NOTE[duckdb]="warehouse volume, CLI in a container"
  elif cmd_exists duckdb; then
    ENGINE_STATE[duckdb]="no warehouse file: set BENCH_DUCKDB_FILE, or run make docker-etl"
  else
    ENGINE_STATE[duckdb]="no DuckDB CLI: make docker-duckdb-cli, or install it (curl https://install.duckdb.org | sh)"
  fi

  # SQL Server: x86_64 only, and the container has to be up.
  if ! engine_supported_here "$ARCH" "$OS" mssql; then
    ENGINE_STATE[mssql]="SQL Server has no build for ${ARCH}: run it on a lab machine"
  elif [ "$docker_ok" -eq 1 ] && container_running "${PROJECT}-mssql"; then
    if cmd_exists sqlcmd; then
      BENCH_SQLCMD="sqlcmd"
      BENCH_MSSQL_HOST="${BENCH_MSSQL_HOST:-127.0.0.1}"; BENCH_MSSQL_PORT="${BENCH_MSSQL_PORT:-51433}"
    else
      BENCH_SQLCMD="docker exec -i -e SQLCMDPASSWORD ${PROJECT}-mssql /opt/mssql-tools18/bin/sqlcmd"
      BENCH_MSSQL_HOST="localhost"; BENCH_MSSQL_PORT="1433"
    fi
    if [ -d "${SQL_DIR}/mssql" ] && [ -n "$(find "${SQL_DIR}/mssql" -name '*.sql' -print -quit 2>/dev/null)" ]; then
      ENGINE_STATE[mssql]="yes"
      ENGINE_NOTE[mssql]="container ${PROJECT}-mssql"
    else
      ENGINE_STATE[mssql]="no T-SQL exported yet: the questions need analytics/<id>/<model>.mssql.sql"
    fi
  elif [ "$docker_ok" -eq 0 ]; then
    ENGINE_STATE[mssql]="$(docker_reason)"
  else
    ENGINE_STATE[mssql]="the container ${PROJECT}-mssql is not running (make docker-mssql)"
  fi
}

psql_query() {
  local client
  read -ra client <<< "${BENCH_PSQL_CMD:-psql}"
  PGPASSWORD="${BENCH_PG_PASSWORD:-store}" "${client[@]}" -h "${BENCH_PG_HOST:-127.0.0.1}" -p "${BENCH_PG_PORT:-55432}" \
    -U "${BENCH_PG_USER:-store}" -d "${BENCH_PG_DB:-store}" -At -c "$1" 2>/dev/null
}

available() {
  local e
  for e in postgres pgduckdb duckdb mssql; do
    [ "${ENGINE_STATE[$e]:-}" = "yes" ] && printf '%s\n' "$e"
  done
}

print_state() {
  local e
  echo
  printf '  %-10s %-4s %s\n' "engine" "" "note"
  for e in postgres pgduckdb duckdb mssql; do
    if [ "${ENGINE_STATE[$e]:-}" = "yes" ]; then
      printf '  %-10s [x]  %s\n' "$e" "${ENGINE_NOTE[$e]:-}"
    else
      printf '  %-10s [ ]  %s\n' "$e" "${ENGINE_STATE[$e]:-unknown}"
    fi
  done
  echo
}

# choose — ask which engines to run when there is a terminal; otherwise take them all.
choose() {
  local all
  all="$(available | tr '\n' ' ')"
  [ -n "${all// /}" ] || die "This machine cannot run any engine yet — see the notes above."
  if [ "$ASSUME_YES" -eq 1 ] || [ ! -t 0 ]; then
    printf '%s' "${all% }" | tr ' ' ','
    return 0
  fi
  local answer
  read -r -p "Run all of: ${all}? [Y/n, or type names separated by a comma] " answer
  case "${answer:-Y}" in
    [Yy]|[Yy][Ee][Ss]|"") printf '%s' "${all% }" | tr ' ' ',' ;;
    [Nn]|[Nn][Oo]) die "Nothing to do." ;;
    *) printf '%s' "${answer// /}" ;;
  esac
}

# ── the run ───────────────────────────────────────────────────

export_sql() {
  [ "$SKIP_EXPORT" -eq 1 ] && { log "keeping the SQL in ${SQL_DIR}"; return 0; }
  have_docker || { warn "docker is not usable: keeping whatever SQL is in ${SQL_DIR}"; return 0; }
  hdr "exporting the questions as SQL"
  run "${COMPOSE[@]}" run --rm --no-deps analytics export-sql --out /data/bench-sql --engines postgres,duckdb,mssql
  rm -rf "${SQL_DIR}"
  mkdir -p "${SQL_DIR}"
  run docker cp "${PROJECT}-analytics:/data/bench-sql/." "${SQL_DIR}/"
  ok "SQL in ${SQL_DIR}"
}

run_engine() {
  local engine="$1" args=()
  args+=(--engine "$engine" --sql-dir "$SQL_DIR" --model "$MODELS" --scale "$SCALE" --repeat "$REPEAT" --warmup "$WARMUP" --out "$OUT_FILE")
  [ -n "$QUESTIONS" ] && args+=(--questions "$QUESTIONS")
  [ "$COLD" -eq 1 ] && args+=(--cold)
  case "$engine" in
    postgres|pgduckdb) args+=(--host "${BENCH_PG_HOST}" --port "${BENCH_PG_PORT}" --db "${BENCH_PG_DB:-store}" --user "${BENCH_PG_USER:-store}") ;;
    duckdb) args+=(--duckdb-file "${BENCH_DUCKDB_FILE}") ;;
    mssql) args+=(--mssql-host "${BENCH_MSSQL_HOST}" --mssql-port "${BENCH_MSSQL_PORT}" --mssql-db "${BENCH_MSSQL_DB:-duckstore}" --mssql-user "${BENCH_MSSQL_USER:-sa}") ;;
  esac
  hdr "$engine"
  BENCH_DUCKDB_CMD="${BENCH_DUCKDB_CMD:-duckdb}" BENCH_PSQL_CMD="${BENCH_PSQL_CMD:-psql}" BENCH_SQLCMD="${BENCH_SQLCMD:-sqlcmd}" \
    "${SCRIPT_DIR}/duckstore-bench-run.sh" "${args[@]}"
}

# summary FILE — median per question and engine, from the TSV rows of this run.
summary() {
  [ -s "$1" ] || return 0
  hdr "median ms per question"
  awk -F'\t' '
    { key = $5 SUBSEP $3; n[key]++; v[key, n[key]] = $7; engines[$3] = 1; questions[$5] = 1 }
    function median(k,   i, c, a, t, j) {
      c = n[k]; if (c == 0) return ""
      for (i = 1; i <= c; i++) a[i] = v[k, i] + 0
      for (i = 2; i <= c; i++) { t = a[i]; for (j = i - 1; j >= 1 && a[j] > t; j--) a[j+1] = a[j]; a[j+1] = t }
      return (c % 2) ? a[(c + 1) / 2] : (a[c/2] + a[c/2 + 1]) / 2
    }
    END {
      printf "  %-38s", "question"
      for (e in engines) printf "%14s", e
      printf "\n"
      for (q in questions) {
        printf "  %-38s", q
        for (e in engines) { m = median(q SUBSEP e); printf "%14s", (m == "" ? "-" : sprintf("%.1f", m)) }
        printf "\n"
      }
    }' "$1" | sort
}

main() {
  while [ $# -gt 0 ]; do
    case "$1" in
      --list) LIST_ONLY=1; shift ;;
      --engines) ENGINES="${2:-}"; shift 2 ;;
      --questions) QUESTIONS="${2:-}"; shift 2 ;;
      --model|--models) MODELS="${2:-}"; shift 2 ;;
      --repeat) REPEAT="${2:-}"; shift 2 ;;
      --warmup) WARMUP="${2:-}"; shift 2 ;;
      --scale) SCALE="${2:-}"; shift 2 ;;
      --cold) COLD=1; shift ;;
      --skip-export) SKIP_EXPORT=1; shift ;;
      --yes|-y) ASSUME_YES=1; shift ;;
      --out) OUT_FILE="${2:-}"; shift 2 ;;
      --sql-dir) SQL_DIR="${2:-}"; shift 2 ;;
      --dry-run) export DRY_RUN=1; shift ;;
      -h|--help) usage; return 0 ;;
      *) usage; die "Unknown argument: $1" ;;
    esac
  done

  hdr "this machine: ${OS} ${ARCH}, $( [ "$(uname -s)" = "Linux" ] && nproc || sysctl -n hw.ncpu 2>/dev/null || echo '?') cpus"
  detect
  print_state
  [ "$LIST_ONLY" -eq 1 ] && return 0

  [ -n "$ENGINES" ] || ENGINES="$(choose)"
  local engine
  for engine in ${ENGINES//,/ }; do
    [ "${ENGINE_STATE[$engine]:-}" = "yes" ] || die "${engine}: ${ENGINE_STATE[$engine]:-unknown engine}"
  done

  [ -n "$OUT_FILE" ] || OUT_FILE="$(default_out local)"
  export_sql

  for engine in ${ENGINES//,/ }; do
    run_engine "$engine"
  done

  summary "$OUT_FILE"
  ok "results in ${OUT_FILE}"
}

[[ "${BASH_SOURCE[0]}" == "$0" ]] && main "$@"
