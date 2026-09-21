#!/usr/bin/env bash
#
# overnight-scale20.sh — build scale 20 and benchmark every engine on it, unattended
#
# Scale 5 fits in memory on this laptop, so those numbers compare execution. Scale 20 is
# 50 million orders and about 106 million order lines: past every engine's buffer pool, which
# is where the curves bend. That takes hours, so this runs the whole chain in order and
# remembers what it finished:
#
#   ./overnight-scale20.sh                 # run everything that is not done yet
#   ./overnight-scale20.sh --from bench    # start again at one step
#   ./overnight-scale20.sh --list          # the steps and what is done
#
# It replaces the data in Postgres: the scale 5 results are already written up in
# docs/results/, and the load test's extra orders go with it.
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/common.sh
. "${SCRIPT_DIR}/lib/common.sh"

SCALE="${SCALE:-20}"
REPEAT="${REPEAT:-3}"
WARMUP="${WARMUP:-1}"
TIMEOUT="${TIMEOUT:-1800}"
STATE="${BENCH_DIR}/.overnight-scale20.state"
LOGS="${BENCH_DIR}/logs"
OUT_TSV="${BENCH_REPO_DIR}/docs/results/bench-star-scale${SCALE}-laptop.tsv"
STEPS=(seed etl star mssql sql verify bench charts)
FROM=""

usage() { cat <<EOF
Usage: $0 [--from STEP] [--list] [--scale N]

Steps, in order: ${STEPS[*]}
  seed    load scale ${SCALE} into Postgres (replaces what is there)
  etl     build the DuckDB warehouse from it
  star    copy the star schema into Postgres (dw.*)
  mssql   load SQL Server: store tables rowstore, star schema columnstore
  sql     export the questions as SQL with the new watermark
  verify  three questions on every engine must return the same rows
  bench   the 15 star-schema questions on all four engines
  charts  redraw docs/results/charts.html

Environment: SCALE=${SCALE} REPEAT=${REPEAT} WARMUP=${WARMUP} TIMEOUT=${TIMEOUT}
EOF
}

done_with() { grep -qx "$1" "$STATE" 2>/dev/null; }
mark_done() { echo "$1" >> "$STATE"; }
step_log()  { printf '%s/%s.log' "$LOGS" "$1"; }

# run_step NAME COMMAND... — skip what is finished, time what is not, stop the night on a failure.
run_step() {
  local name="$1"; shift
  if [ -n "$FROM" ]; then
    [ "$name" = "$FROM" ] && FROM="" || { log "skip ${name} (before --from)"; return 0; }
  elif done_with "$name"; then
    log "skip ${name} (already done)"
    return 0
  fi

  hdr "${name} — started $(date -u +%H:%M:%SZ)"
  local start=$SECONDS
  if ! "$@" >>"$(step_log "$name")" 2>&1; then
    err "${name} failed after $(( (SECONDS - start) / 60 )) min — see $(step_log "$name")"
    tail -5 "$(step_log "$name")" >&2 || true
    exit 1
  fi
  mark_done "$name"
  ok "${name} took $(( (SECONDS - start) / 60 )) min"
}

compose() { (cd "$BENCH_REPO_DIR" && docker compose "$@" < /dev/null); }

# --build on the seeder: an image from before the last schema change seeds data the ETL cannot
# read. That cost one night: fx_rates_daily was missing and the extract stopped at the first table.
step_seed()  { compose run --rm --build seed -scale "$SCALE"; }
step_etl()   { compose run --rm --no-deps analytics etl; }
step_star()  { compose run --rm --no-deps analytics star-to-postgres; }
step_mssql() { compose run --rm --no-deps analytics mssql-load; }

step_sql() {
  compose run --rm --no-deps analytics export-sql --out /data/bench-sql --engines postgres,duckdb,mssql
  rm -rf "${BENCH_DIR}/sql"
  mkdir -p "${BENCH_DIR}/sql"
  (cd "$BENCH_REPO_DIR" && docker cp duckstore-analytics:/data/bench-sql/. bench/sql/)
}

step_verify() {
  local questions="active-customers,brand-returns,monthly-revenue"
  bash "${SCRIPT_DIR}/duckstore-bench-verify.sh" --engine duckdb --model star --questions "$questions" --timeout "$TIMEOUT"
  bash "${SCRIPT_DIR}/duckstore-bench-verify.sh" --engine mssql --model star --questions "$questions" --timeout "$TIMEOUT"
}

step_bench() {
  local engine
  for engine in postgres pgduckdb duckdb mssql; do
    bash "${SCRIPT_DIR}/duckstore-bench-run.sh" --engine "$engine" --model star --sql-dir "${BENCH_DIR}/sql" \
      --scale "$SCALE" --repeat "$REPEAT" --warmup "$WARMUP" --timeout "$TIMEOUT" --out "$OUT_TSV"
  done
}

step_charts() { (cd "$BENCH_REPO_DIR" && python3 bench/make-charts.py); }

list_steps() {
  local step state
  for step in "${STEPS[@]}"; do
    state="pending"
    done_with "$step" && state="done"
    printf '  %-8s %s\n' "$step" "$state"
  done
}

main() {
  while [ $# -gt 0 ]; do
    case "$1" in
      --from) FROM="${2:-}"; shift 2 ;;
      --scale) SCALE="${2:-}"; shift 2 ;;
      --list) list_steps; return 0 ;;
      -h|--help) usage; return 0 ;;
      *) usage; die "Unknown argument: $1" ;;
    esac
  done

  mkdir -p "$LOGS"
  : > "$(step_log run)" || true
  hdr "scale ${SCALE}, started $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  log "logs in ${LOGS}, state in ${STATE}"

  run_step seed   step_seed
  run_step etl    step_etl
  run_step star   step_star
  run_step mssql  step_mssql
  run_step sql    step_sql
  run_step verify step_verify
  run_step bench  step_bench
  run_step charts step_charts

  ok "everything finished $(date -u +%Y-%m-%dT%H:%M:%SZ) — results in ${OUT_TSV}"
}

[[ "${BASH_SOURCE[0]}" == "$0" ]] && main "$@"
