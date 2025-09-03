#!/usr/bin/env bash
set -Eeuo pipefail
IFS=$'\n\t'

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# Find repo root by searching for go.mod upwards from the script directory
find_repo_root(){
	local d="$SCRIPT_DIR"
	while :; do
		if [ -f "$d/go.mod" ]; then
			echo "$d"; return 0
		fi
		[ "$d" = "/" ] && break
		d="$(dirname "$d")"
	done
	(cd "$SCRIPT_DIR/../.." && pwd)
}
REPO_ROOT="$(find_repo_root)"
BIN_DIR="$REPO_ROOT/bin"
mkdir -p "$BIN_DIR"

ts(){ date '+%H:%M:%S'; }
log(){ printf '[%s] %s\n' "$(ts)" "$*"; }
die(){ printf '[%s] ERR: %s\n' "$(ts)" "$*" >&2; exit 1; }

command -v jq >/dev/null 2>&1 || die "jq is required"

log "Building binaries…"
(cd "$REPO_ROOT" && go build -o "$BIN_DIR/pokerctl" ./cmd/pokerctl)
(cd "$REPO_ROOT" && go build -o "$BIN_DIR/pokersrv" ./cmd/pokersrv)

workdir=$(mktemp -d)
PORTFILE="$workdir/port"
DB="$workdir/poker.sqlite"
SRV_PID=""

cleanup(){
	if [[ -n "$SRV_PID" ]]; then
		kill "$SRV_PID" 2>/dev/null || true
		wait "$SRV_PID" 2>/dev/null || true
	fi
	rm -rf "$workdir" || true
}
trap cleanup EXIT

log "Starting server…"
"$BIN_DIR/pokersrv" -db "$DB" -host 127.0.0.1 -port 0 -portfile "$PORTFILE" &
SRV_PID=$!
for i in {1..50}; do [[ -s "$PORTFILE" ]] && break; sleep 0.1; done
[[ -s "$PORTFILE" ]] || die "server did not write portfile"
PORT=$(cat "$PORTFILE")
[[ -n "$PORT" ]] || die "empty port"

export GRPCHOST=127.0.0.1
export GRPCPORT="$PORT"

pc(){ local dir="$1"; shift; "$BIN_DIR/pokerctl" -offline -grpcinsecure -grpchost "$GRPCHOST" -grpcport "$GRPCPORT" -datadir "$dir" "$@"; }

wait_until_preflop(){
	# args: datadir table_id timeout_seconds
	local dir="$1"; local tid="$2"; local to="$3"
	local end=$((SECONDS+to))
	local out=""
	while (( SECONDS < end )); do
		out=$(pc "$dir" state --table-id "$tid" || true)
		json=$(printf '%s\n' "$out" | sed -n '/^{/,$p')
		if echo "$json" | jq -e '(.game_started==true) and ((.phase_name=="PRE_FLOP") or (.phase==2))' >/dev/null 2>&1; then
			printf '%s\n' "$json"
			return 0
		fi
		sleep 0.2
	done
	printf '%s\n' "$json"
	return 1
}

assert_jq_true(){
	local json="$1"; local expr="$2"; local msg="$3"
	echo "$json" | jq -e "$expr" >/dev/null 2>&1 || { echo "$json" | jq .; die "$msg"; }
}

SMALL=5
BIG=10

# Test 1: Heads-up blinds (dealer is small blind; non-dealer posts big blind; current player is dealer)
log "[HU] Creating 2-player table…"
P1="$workdir/hu_p1"; P2="$workdir/hu_p2"; mkdir -p "$P1" "$P2"
T1=$(pc "$P1" create-table --min-players 2 --max-players 2 --small-blind "$SMALL" --big-blind "$BIG" | grep -E '^table_')
[[ -n "$T1" ]] || die "failed to create 2p table"
pc "$P2" join --table-id "$T1"
pc "$P1" ready --table-id "$T1" set
pc "$P2" ready --table-id "$T1" set

log "[HU] Waiting for PRE_FLOP…"
HU_JSON=$(wait_until_preflop "$P1" "$T1" 10) || die "HU did not reach PRE_FLOP"

# Validate HU blinds via bets and current player
assert_jq_true "$HU_JSON" '.players|length==2' "HU: players length != 2"
assert_jq_true "$HU_JSON" "(.players|map(select((.current_bet // 0) == ${SMALL}))|length)==1" "HU: exactly one small blind"
assert_jq_true "$HU_JSON" "(.players|map(select((.current_bet // 0) == ${BIG}))|length)==1" "HU: exactly one big blind"
assert_jq_true "$HU_JSON" ". as \$r | (\$r.players[] | select(.id == \$r.current_player) | (.current_bet // 0)) == ${SMALL}" "HU: current player should be small blind preflop"
log "[HU] Blinds validated."

# Test 2: 3-player blinds (dealer posts no blind; next left small blind; then big blind)
log "[3P] Creating 3-player table…"
Q1="$workdir/p3_p1"; Q2="$workdir/p3_p2"; Q3="$workdir/p3_p3"; mkdir -p "$Q1" "$Q2" "$Q3"
T2=$(pc "$Q1" create-table --min-players 3 --max-players 3 --small-blind "$SMALL" --big-blind "$BIG" | grep -E '^table_')
[[ -n "$T2" ]] || die "failed to create 3p table"
pc "$Q2" join --table-id "$T2"
pc "$Q3" join --table-id "$T2"
pc "$Q1" ready --table-id "$T2" set
pc "$Q2" ready --table-id "$T2" set
pc "$Q3" ready --table-id "$T2" set

log "[3P] Waiting for PRE_FLOP…"
THREE_JSON=$(wait_until_preflop "$Q1" "$T2" 10) || die "3P did not reach PRE_FLOP"

# Validate 3P blinds via bets and current player
assert_jq_true "$THREE_JSON" '.players|length==3' "3P: players length != 3"
assert_jq_true "$THREE_JSON" "(.players|map(select((.current_bet // 0) == 0))|length)==1" "3P: exactly one player with 0 preflop bet (dealer)"
assert_jq_true "$THREE_JSON" "(.players|map(select((.current_bet // 0) == ${SMALL}))|length)==1" "3P: exactly one small blind"
assert_jq_true "$THREE_JSON" "(.players|map(select((.current_bet // 0) == ${BIG}))|length)==1" "3P: exactly one big blind"
assert_jq_true "$THREE_JSON" ". as \$r | (\$r.players[] | select(.id == \$r.current_player) | (.current_bet // 0)) == 0" "3P: current player should act after big blind (bet 0)"
log "[3P] Blinds validated."

# Success
log "All blind correctness checks passed."
echo "OK"
exit 0


