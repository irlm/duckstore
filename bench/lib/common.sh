#!/usr/bin/env bash
# ============================================================
# bench/lib/common.sh — shared helpers for the duckstore bench scripts
#
# Source at the top of every script:
#   SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
#   # shellcheck source=lib/common.sh
#   . "${SCRIPT_DIR}/lib/common.sh"
#
# Same shape as NetworkLab's lab-setup/scripts/lib/common.sh, so the two sets of
# scripts read alike and can share a lab: logging, secrets from the environment or
# /etc/lab-secrets.env, SSH options, and a dry-run wrapper. Sourcing this file also
# loads bench.conf next to it, if there is one (git-ignored: it holds host names).
# It never calls `set -e` itself — callers do that.
# ============================================================

[ -n "${BENCH_COMMON_LOADED:-}" ] && return 0
BENCH_COMMON_LOADED=1

BENCH_LIB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BENCH_DIR="$(cd "${BENCH_LIB_DIR}/.." && pwd)"
# shellcheck disable=SC2034  # used by scripts that source this file
BENCH_REPO_DIR="$(cd "${BENCH_DIR}/.." && pwd)"

# shellcheck source=/dev/null
[ -f "${BENCH_DIR}/bench.conf" ] && . "${BENCH_DIR}/bench.conf"

# ── Logging ───────────────────────────────────────────────────
if [ -t 2 ]; then
  _C_RED=$'\033[0;31m'; _C_GRN=$'\033[0;32m'; _C_YEL=$'\033[0;33m'; _C_BLU=$'\033[0;34m'; _C_OFF=$'\033[0m'
else
  _C_RED=''; _C_GRN=''; _C_YEL=''; _C_BLU=''; _C_OFF=''
fi
log()  { echo "${_C_BLU}INFO:${_C_OFF} $*" >&2; }
ok()   { echo "${_C_GRN}OK:${_C_OFF}   $*" >&2; }
warn() { echo "${_C_YEL}WARN:${_C_OFF} $*" >&2; }
err()  { echo "${_C_RED}ERROR:${_C_OFF} $*" >&2; }
die()  { err "$@"; exit 1; }
hdr()  { echo; echo "${_C_BLU}==> $*${_C_OFF}" >&2; }

# ── Predicates ────────────────────────────────────────────────
cmd_exists()   { command -v "$1" >/dev/null 2>&1; }
require_cmd()  { cmd_exists "$1" || die "$1 is required but not installed${2:+ ($2)}."; }
is_root()      { [ "$(id -u)" -eq 0 ]; }
require_root() { is_root || die "This script must be run as root (use sudo)."; }

# run CMD... — honours DRY_RUN=1 and fails loudly.
run() {
  if [ "${DRY_RUN:-0}" -eq 1 ]; then log "[DRY-RUN] $*"; return 0; fi
  "$@" || die "Command failed: $*"
}

# ── SSH ───────────────────────────────────────────────────────
# Same options as the lab scripts: no prompts, short timeout, optional jump host
# (BENCH_SSH_JUMP), because the lab subnet may not be routed from the laptop.
BENCH_SSH_OPTS=(-o BatchMode=yes -o ConnectTimeout=15)
bench_ssh() {
  local host="$1"; shift
  local opts=("${BENCH_SSH_OPTS[@]}")
  [ -n "${BENCH_SSH_JUMP:-}" ] && opts+=(-J "${BENCH_SSH_JUMP}")
  ssh "${opts[@]}" "$host" "$@"
}

# ── Secrets ───────────────────────────────────────────────────
# read_secret VARNAME [PROMPT] — environment → $BENCH_SECRETS_FILE (default
# /etc/lab-secrets.env, mode 600) → tty prompt. Never a command-line flag: an
# argument is visible to every user in `ps`.
read_secret() {
  local var="$1" prompt="${2:-$1}" file="${BENCH_SECRETS_FILE:-${LAB_SECRETS_FILE:-/etc/lab-secrets.env}}"
  [ -n "${!var:-}" ] && return 0
  if [ -f "$file" ]; then
    local mode; mode="$(stat -c %a "$file" 2>/dev/null || echo 600)"
    [ "$mode" = "600" ] || [ "$mode" = "400" ] || die "$file must be mode 600 (is $mode)"
    # shellcheck disable=SC1090
    . "$file"
    [ -n "${!var:-}" ] && return 0
  fi
  if [ -t 0 ]; then
    local val
    read -r -s -p "${prompt}: " val; echo >&2
    [ -n "$val" ] || die "$var is required"
    printf -v "$var" '%s' "$val"
    return 0
  fi
  die "$var not set (export it, put it in $file, or run interactively)"
}

# password_forbidden PASS — 0 if PASS is a known default that must not be used.
password_forbidden() {
  case "$1" in ''|'P@ssw0rd!'|'P@ssw0rd'|'password'|'changeme'|'changeme123'|'admin'|'admin123'|'postgres') return 0 ;; esac
  return 1
}

# ── Results ───────────────────────────────────────────────────
# Benchmark results are tab separated, without a header, and land in the home
# directory as results-*.tsv, so NetworkLab's duckbench-collect.sh picks them up
# unchanged. Two row shapes, the same two duckbench writes:
#   7 columns  iso_time host engine scale query rep ms      (one process, engine-reported time)
#   6 columns  iso_time host engine worker query ms         (concurrent clients, end-to-end time)
iso_now()      { date -u +%Y-%m-%dT%H:%M:%SZ; }
bench_stamp()  { date -u +%Y%m%dT%H%M%SZ; }
default_out()  { printf '%s/results-duckstore-%s-%s.tsv' "$HOME" "$1" "$(bench_stamp)"; }
