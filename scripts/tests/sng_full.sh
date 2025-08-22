#!/usr/bin/env bash
set -Eeuo pipefail
IFS=$'\n\t'

# Full SNG-style smoke test:
# - Seeds balances for 3 players
# - Creates a 3-max table with buy-in and starting chips
# - Autoplays many hands to SHOWDOWN
# - Validates winners presence and pot > 0 per hand
# - Verifies DCR balances stay constant post buy-in (current server behavior)
#
# Notes:
#   The current server resets poker chip stacks every hand and does not payout
#   tournament winnings to DCR balances. This script therefore asserts that
#   DCR balances remain unchanged after the initial buy-in. When payouts are
#   implemented, set EXPECT_PAYOUT=true and adjust checks accordingly.

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

# Configurables
HANDS_TO_PLAY="${HANDS_TO_PLAY:-15}"
INITIAL_BANKROLL="${INITIAL_BANKROLL:-10000}"
BUY_IN="${BUY_IN:-1000}"
SMALL_BLIND="${SMALL_BLIND:-10}"
BIG_BLIND="${BIG_BLIND:-20}"
STARTING_CHIPS="${STARTING_CHIPS:-1000}"
EXPECT_PAYOUT="${EXPECT_PAYOUT:-false}"
# Deterministic RNG seed (override with POKER_SEED env var if desired)
SEED="${POKER_SEED:-42}"

# Build binaries
log "Building binaries…"
(cd "$REPO_ROOT" && go build -o "$BIN_DIR/pokerctl" ./cmd/pokerctl)
(cd "$REPO_ROOT" && go build -o "$BIN_DIR/pokersrv" ./cmd/pokersrv)

# Temp workspace
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

# Start server
log "Starting server… (seed=$SEED)"
"$BIN_DIR/pokersrv" -db "$DB" -host 127.0.0.1 -port 0 -portfile "$PORTFILE" -seed "$SEED" -autostartms 300 &
SRV_PID=$!
for i in {1..50}; do [[ -s "$PORTFILE" ]] && break; sleep 0.1; done
[[ -s "$PORTFILE" ]] || die "server did not write portfile"
PORT=$(cat "$PORTFILE")
[[ -n "$PORT" ]] || die "empty port"

export GRPCHOST=127.0.0.1
export GRPCPORT="$PORT"

# Deterministic id from datadir path
derive_id(){
    local dir="$1"
    # Use sha256sum if available; fallback to md5sum
    if command -v sha256sum >/dev/null 2>&1; then
        local h
        h=$(printf '%s' "$dir" | sha256sum | awk '{print $1}')
        echo "cid-${h:0:16}"
    else
        local h
        h=$(printf '%s' "$dir" | md5sum | awk '{print $1}')
        echo "cid-${h:0:16}"
    fi
}

# pokerctl wrapper: pc <datadir> <args...>
pc(){ local dir="$1"; shift; local id; id=$(derive_id "$dir"); "$BIN_DIR/pokerctl" -offline -grpcinsecure -grpchost "$GRPCHOST" -grpcport "$GRPCPORT" -datadir "$dir" -id "$id" -debug error "$@"; }

# Helpers
get_balance(){
	local dir="$1"
	local out num
	out=$(pc "$dir" balance 2>/dev/null || true)
	# Prefer the last line that contains only digits
	num=$(printf '%s\n' "$out" | awk '/^[0-9]+$/{val=$1} END{if (val!="") print val}')
	if [[ -z "${num:-}" ]]; then
		# Fallback: last numeric token anywhere
		num=$(printf '%s\n' "$out" | grep -Eo '[0-9]+' | tail -n1)
	fi
	printf '%s\n' "${num:-0}"
}

set_balance(){
	local dir="$1"; local target="$2"
	local curr; curr=$(get_balance "$dir" || echo 0)
	local delta=$((target - curr))
	if (( delta != 0 )); then
		pc "$dir" balance --add "$delta" >/dev/null
	fi
}

state_json(){ local dir="$1"; local tid="$2"; pc "$dir" state --table-id "$tid" | sed -n '/^{/,$p'; }

print_state(){
	# args: dir table_id label
	local dir="$1"; local tid="$2"; local label="${3:-}"
	local js; js=$(state_json "$dir" "$tid" || true)
	local phase; phase=$(echo "$js" | jq -r '.phase_name // .phase // ""' 2>/dev/null || true)
	local pot; pot=$(echo "$js" | jq -r '.pot // 0' 2>/dev/null || echo 0)
	log "STATE${label:+ ($label)} phase=$phase pot=$pot"
	# Pretty print full state
	echo "$js" | jq '.' 2>/dev/null || echo "$js"
}

wait_phase(){
	# args: dir table_id phase_name timeout_seconds
	local dir="$1"; local tid="$2"; local phase="$3"; local to="$4"
	local end=$((SECONDS+to))
	local json=""
	while (( SECONDS < end )); do
		json=$(state_json "$dir" "$tid" || true)
		if echo "$json" | jq -e ".phase_name == \"$phase\" or (.phase == 6 and \"$phase\" == \"SHOWDOWN\")" >/dev/null 2>&1; then
			printf '%s\n' "$json"; return 0
		fi
		sleep 0.2
	done
	printf '%s\n' "$json"; return 1
}

wait_started(){
	# args: dir table_id timeout_seconds
	local dir="$1"; local tid="$2"; local to="$3"
	local end=$((SECONDS+to))
	local json=""
	while (( SECONDS < end )); do
		json=$(state_json "$dir" "$tid" || true)
		if echo "$json" | jq -e '.game_started == true' >/dev/null 2>&1; then
			printf '%s\n' "$json"; return 0
		fi
		sleep 0.2
	done
	printf '%s\n' "$json"; return 1
}

wait_winners(){
	# args: dir table_id timeout_seconds
	local dir="$1"; local tid="$2"; local to="$3"
	local end=$((SECONDS+to))
	local json=""
	while (( SECONDS < end )); do
		json=$(state_json "$dir" "$tid" || true)
		if echo "$json" | jq -e '(.winners | length) >= 1' >/dev/null 2>&1; then
			printf '%s\n' "$json"; return 0
		fi
		sleep 0.2
	done
	printf '%s\n' "$json"; return 1
}

# Query winners via RPC once
winners_json(){
	local dir="$1"; local tid="$2"
	pc "$dir" last-winners --table-id "$tid" 2>/dev/null || true
}

wait_winners_rpc(){
	# args: dir table_id timeout_seconds
	local dir="$1"; local tid="$2"; local to="$3"
	local end=$((SECONDS+to))
	local json=""
	while (( SECONDS < end )); do
		json=$(winners_json "$dir" "$tid")
		# Expect fields: .winners (array), .pot (int)
		if echo "$json" | jq -e '(.winners | length) >= 1 and (.pot // 0) > 0' >/dev/null 2>&1; then
			printf '%s\n' "$json"; return 0
		fi
		sleep 0.2
	done
	printf '%s\n' "$json"; return 1
}

# Players
P1="$workdir/p1"; P2="$workdir/p2"; P3="$workdir/p3"; mkdir -p "$P1" "$P2" "$P3"

# Seed balances
log "Seeding balances to $INITIAL_BANKROLL…"
set_balance "$P1" "$INITIAL_BANKROLL"
set_balance "$P2" "$INITIAL_BANKROLL"
set_balance "$P3" "$INITIAL_BANKROLL"

# Create SNG-like table
log "Creating 3-player table with buy-in $BUY_IN…"
TABLE_ID=$(pc "$P1" create-table --min-players 3 --max-players 3 --buy-in "$BUY_IN" --min-balance "$BUY_IN" --small-blind "$SMALL_BLIND" --big-blind "$BIG_BLIND" --starting-chips "$STARTING_CHIPS" --time-bank-seconds 1 | grep -E '^table_')
[[ -n "$TABLE_ID" ]] || die "failed to create table"
log "Table: $TABLE_ID"

# Join and ready
pc "$P2" join --table-id "$TABLE_ID"
pc "$P3" join --table-id "$TABLE_ID"
pc "$P1" ready --table-id "$TABLE_ID" set
pc "$P2" ready --table-id "$TABLE_ID" set
pc "$P3" ready --table-id "$TABLE_ID" set

# Ensure game started before autoplaying
wait_started "$P1" "$TABLE_ID" 10 >/dev/null || die "game did not start"

# Keep game streams open for all players to ensure timely state updates
pc "$P1" stream --table-id "$TABLE_ID" >/dev/null 2>&1 & SP1=$!
pc "$P2" stream --table-id "$TABLE_ID" >/dev/null 2>&1 & SP2=$!
pc "$P3" stream --table-id "$TABLE_ID" >/dev/null 2>&1 & SP3=$!
cleanup_streams(){ kill $SP1 $SP2 $SP3 2>/dev/null || true; }
trap cleanup_streams EXIT

# Check post buy-in balances
bal1_post=$(get_balance "$P1")
bal2_post=$(get_balance "$P2")
bal3_post=$(get_balance "$P3")
[[ "$bal1_post" -eq $((INITIAL_BANKROLL - BUY_IN)) ]] || die "P1 post-buyin balance mismatch: $bal1_post"
[[ "$bal2_post" -eq $((INITIAL_BANKROLL - BUY_IN)) ]] || die "P2 post-buyin balance mismatch: $bal2_post"
[[ "$bal3_post" -eq $((INITIAL_BANKROLL - BUY_IN)) ]] || die "P3 post-buyin balance mismatch: $bal3_post"

#############################
# Deterministic action driver
#############################

# Resolve the acting player's datadir dynamically by comparing IDs each step
dir_for_id(){
	local pid="$1"
	local i
	for i in "$P1" "$P2" "$P3"; do
		local cid
		cid=$(pc "$i" id 2>/dev/null | grep -E '^cid-' | head -n1)
		if [[ "$cid" == "$pid" ]]; then
			echo "$i"
			return 0
		fi
	done
	echo ""
}

# Issue a single sensible action for the current player to guarantee progress
step_once(){
	# args: table_id
	local tid="$1"
	local js cp curr_bet p_bet act_dir
	js=$(state_json "$P1" "$tid" || true)
	cp=$(echo "$js" | jq -r '.current_player // ""')
	[[ -n "$cp" ]] || return 1
	act_dir=$(dir_for_id "$cp")
	[[ -n "$act_dir" ]] || return 1
	# Ensure this process has the table context set
	# pc "$act_dir" join --table-id "$tid" >/dev/null 2>&1 || true
	curr_bet=$(echo "$js" | jq -r '.current_bet // 0')
	# find this player's current_bet from players[]
	p_bet=$(echo "$js" | jq -r --arg id "$cp" '.players[] | select(.id==$id) | (.current_bet // 0)')
	# Decide action: call if behind; otherwise check
	if [[ -z "$p_bet" ]]; then p_bet=0; fi
	if (( p_bet < curr_bet )); then
		# Match to current bet explicitly
		if ! pc "$act_dir" act --table-id "$tid" bet "$curr_bet"; then
			print_state "$P1" "$tid" "act_bet_failed"; return 1
		fi
	else
		if ! pc "$act_dir" act --table-id "$tid" check; then
			print_state "$P1" "$tid" "act_check_failed"; return 1
		fi
	fi
	# Small delay to let server advance
	sleep 0.2
	return 0
}

play_hand_to_showdown(){
	# args: table_id timeout_seconds
	local tid="$1"; local to="$2"
	local end=$((SECONDS+to))
	while (( SECONDS < end )); do
		# If already at showdown, we're done
		if state_json "$P1" "$tid" | jq -e '.phase_name=="SHOWDOWN" or .phase==6' >/dev/null 2>&1; then
			return 0
		fi
		step_once "$tid" || sleep 0.2
	done
	return 1
}

# Drive hands deterministically (call/check progression) to guaranteed showdown
log "Playing $HANDS_TO_PLAY hands with deterministic driver…"
declare -A wins; wins["p1"]=0; wins["p2"]=0; wins["p3"]=0
for (( hand=1; hand<=HANDS_TO_PLAY; hand++ )); do
	log "Hand #$hand"
	# Ensure game progresses to SHOWDOWN within timeout
	play_hand_to_showdown "$TABLE_ID" 20 || { print_state "$P1" "$TABLE_ID" "wait_phase_timeout"; die "did not reach SHOWDOWN"; }
	# Snapshot after showdown
	J=$(state_json "$P1" "$TABLE_ID")
	log "Reached SHOWDOWN; dumping state snapshot"
	echo "$J" | jq '.' 2>/dev/null || echo "$J"
	# Validate pot and winners via authoritative RPC
	pot=$(echo "$J" | jq -r '.pot // 0')
	[[ "$pot" -gt 0 ]] || { print_state "$P1" "$TABLE_ID" "pot_check_failed"; die "pot not > 0 at showdown"; }
	LW=$(winners_json "$P1" "$TABLE_ID")
	winners_count=$(echo "$LW" | jq -r '.winners | length')
	[[ "$winners_count" -ge 1 ]] || { echo "$LW" | jq '.'; die "no winners reported by GetLastWinners"; }
	# Pot conservation: sum(winnings) must equal pot
	sum_winnings=$(echo "$LW" | jq '[.winners[].winnings] | add // 0')
	if [[ "$sum_winnings" != "$pot" ]]; then
		printf '[%s] ERR: pot conservation failed: pot=%s sum_winnings=%s\n' "$(ts)" "$pot" "$sum_winnings" >&2
		echo "$LW" | jq '.'
		die "sum(winnings) != pot"
	fi
	log "Showdown pot=$pot, winners_count=$winners_count"
done

# Final balance assertions (current behavior: no payouts to DCR balances)
final1=$(get_balance "$P1")
final2=$(get_balance "$P2")
final3=$(get_balance "$P3")

if [[ "$EXPECT_PAYOUT" == "true" ]]; then
	# Placeholder: when payouts are implemented, adapt the logic below to expected prize distribution
	log "EXPECT_PAYOUT=true set, but payout expectations are not yet defined. Current balances: $final1, $final2, $final3"
else
	[[ "$final1" -eq "$bal1_post" ]] || die "P1 balance changed unexpectedly: $bal1_post -> $final1"
	[[ "$final2" -eq "$bal2_post" ]] || die "P2 balance changed unexpectedly: $bal2_post -> $final2"
	[[ "$final3" -eq "$bal3_post" ]] || die "P3 balance changed unexpectedly: $bal3_post -> $final3"
fi

log "Completed $HANDS_TO_PLAY hands. DCR balances verified."
echo "OK"

