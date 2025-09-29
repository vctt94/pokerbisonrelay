#!/usr/bin/env bash
set -Eeuo pipefail
IFS=$'\n\t'

# Full SNG-style smoke test, now event-driven for terminal detection.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
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

# Config
HANDS_TO_PLAY="${HANDS_TO_PLAY:-15}"
INITIAL_BANKROLL="${INITIAL_BANKROLL:-10000}"
BUY_IN="${BUY_IN:-1000}"
SMALL_BLIND="${SMALL_BLIND:-10}"
BIG_BLIND="${BIG_BLIND:-20}"
STARTING_CHIPS="${STARTING_CHIPS:-1000}"
EXPECT_PAYOUT="${EXPECT_PAYOUT:-false}"
SEED="${POKER_SEED:-42}"
AUTO_START_MS="${AUTO_START_MS:-500}"

# Driver safeguards
MAX_ACTIONS_PER_HAND="${MAX_ACTIONS_PER_HAND:-120}"    # hard cap on actions we perform per hand
STALL_WINDOW_SEC="${STALL_WINDOW_SEC:-2}"               # if no phase change, try a nudge
SAFE_RAISE_CAP="${SAFE_RAISE_CAP:-200}"                 # cap raises so we don't spiral
WAIT_GAME_STARTED_TIMEOUT="${WAIT_GAME_STARTED_TIMEOUT:-1s}"
WAIT_SHOWDOWN_TIMEOUT="${WAIT_SHOWDOWN_TIMEOUT:-1s}"
WAIT_NEWHAND_TIMEOUT="${WAIT_NEWHAND_TIMEOUT:-1s}"

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

log "Starting server… (seed=$SEED)"
DEBUGLEVEL_INPUT=${DEBUGLEVEL:-info}
"$BIN_DIR/pokersrv" -db "$DB" -host 127.0.0.1 -port 0 -portfile "$PORTFILE" -seed "$SEED" -debuglevel "$DEBUGLEVEL_INPUT" &
SRV_PID=$!
for i in {1..50}; do [[ -s "$PORTFILE" ]] && break; sleep 0.1; done
[[ -s "$PORTFILE" ]] || die "server did not write portfile"
PORT=$(cat "$PORTFILE")
[[ -n "$PORT" ]] || die "empty port"

export GRPCHOST=127.0.0.1
export GRPCPORT="$PORT"

derive_id(){
  local dir="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    printf 'cid-%s\n' "$(printf '%s' "$dir" | sha256sum | awk '{print substr($1,1,16)}')"
  else
    printf 'cid-%s\n' "$(printf '%s' "$dir" | md5sum | awk '{print substr($1,1,16)}')"
  fi
}

pc(){ local dir="$1"; shift; local id; id=$(derive_id "$dir"); "$BIN_DIR/pokerctl" -offline -grpcinsecure -grpchost "$GRPCHOST" -grpcport "$GRPCPORT" -datadir "$dir" -id "$id" -debug error "$@"; }

# --- Helpers (state only for action decisions; events end the hand) ---

state_json(){ local dir="$1"; local tid="$2"; pc "$dir" state --table-id "$tid" | sed -n '/^{/,$p'; }

get_balance(){
  local dir="$1" out num
  out=$(pc "$dir" balance 2>/dev/null || true)
  num=$(printf '%s\n' "$out" | awk '/^[0-9]+$/{val=$1} END{if (val!="") print val}')
  [[ -n "${num:-}" ]] || num=$(printf '%s\n' "$out" | grep -Eo '[0-9]+' | tail -n1)
  printf '%s\n' "${num:-0}"
}

set_balance(){
  local dir="$1" target="$2" curr delta
  curr=$(get_balance "$dir" || echo 0)
  delta=$((target - curr))
  (( delta != 0 )) && pc "$dir" balance --add "$delta" >/dev/null
}

print_state(){
  local dir="$1" tid="$2" label="${3:-}"
  local js phase pot
  js=$(state_json "$dir" "$tid" || true)
  phase=$(echo "$js" | jq -r '.phase_name // .phase // ""' 2>/dev/null || true)
  pot=$(echo "$js" | jq -r '.pot // 0' 2>/dev/null || echo 0)
  log "STATE${label:+ ($label)} phase=$phase pot=$pot"
  echo "$js" | jq '.' 2>/dev/null || echo "$js"
}

# Event waits (use event-driven notifications)
wait_game_started(){
  local dir="$1" tid="$2"
  pc "$dir" wait --type GAME_STARTED --table-id "$tid" --timeout "$WAIT_GAME_STARTED_TIMEOUT" >/dev/null
}

wait_terminal_event() {
  # $1=dir  $2=table_id  $3=timeout (default: 150ms)
  local dir="$1" tid="$2" tmo="${3:-150ms}"
  local ev

  # Fast-drain pending
  ev=$(pc "$dir" wait --type SHOWDOWN_RESULT  --table-id "$tid" --timeout 0s 2>/dev/null || true)
  [[ -n "$ev" ]] && { printf '%s\n' "$ev"; return 0; }
  ev=$(pc "$dir" wait --type NEW_HAND_STARTED --table-id "$tid" --timeout 0s 2>/dev/null || true)
  [[ -n "$ev" ]] && { printf '%s\n' "$ev"; return 0; }

  # Short poll (don’t stack two long waits back-to-back)
  ev=$(pc "$dir" wait --type SHOWDOWN_RESULT  --table-id "$tid" --timeout "$tmo" 2>/dev/null || true)
  [[ -n "$ev" ]] && { printf '%s\n' "$ev"; return 0; }
  ev=$(pc "$dir" wait --type NEW_HAND_STARTED --table-id "$tid" --timeout "$tmo" 2>/dev/null || true)
  [[ -n "$ev" ]] && { printf '%s\n' "$ev"; return 0; }

  return 1
}

# RPC winners (for accounting / conservation checks)
winners_json(){ local dir="$1" tid="$2"; pc "$dir" last-winners --table-id "$tid" 2>/dev/null || true; }

# Tiny utilities for current player/action choice
dir_for_id(){
  local pid="$1" i
  for i in "$P1" "$P2" "$P3"; do
    local cid; cid=$(pc "$i" id 2>/dev/null | grep -E '^cid-' | head -n1)
    [[ "$cid" == "$pid" ]] && { echo "$i"; return 0; }
  done
  echo ""
}

step_once(){
  local tid="$1" js cp curr_bet p_bet player_balance act_dir
  js=$(state_json "$P1" "$tid" || true) || return 1
  cp=$(echo "$js" | jq -r '.current_player // ""')
  [[ -n "$cp" ]] || return 1
  act_dir=$(dir_for_id "$cp")
  [[ -n "$act_dir" ]] || return 1

  curr_bet=$(echo "$js" | jq -r '.current_bet // 0')
  p_bet=$(echo "$js" | jq -r --arg id "$cp" '.players[] | select(.id==$id) | (.current_bet // 0)')
  player_balance=$(echo "$js" | jq -r --arg id "$cp" '.players[] | select(.id==$id) | .balance // 0')
  [[ -n "$p_bet" ]] || p_bet=0

  # temper raises
  local raise_to capped_to
  raise_to=$((curr_bet + BIG_BLIND))
  (( raise_to > curr_bet + SAFE_RAISE_CAP )) && raise_to=$((curr_bet + SAFE_RAISE_CAP))
  capped_to=$(( raise_to > player_balance ? player_balance : raise_to ))

  if (( p_bet < curr_bet )); then
    if (( player_balance >= (curr_bet - p_bet) )); then
      if (( RANDOM % 10 < 7 )); then
        pc "$act_dir" act --table-id "$tid" call || { print_state "$P1" "$tid" "act_call_failed"; return 1; }
      else
        if (( capped_to > curr_bet )); then
          pc "$act_dir" act --table-id "$tid" raise "$capped_to" || pc "$act_dir" act --table-id "$tid" call || true
        else
          pc "$act_dir" act --table-id "$tid" call || true
        fi
      fi
    else
      pc "$act_dir" act --table-id "$tid" fold || { print_state "$P1" "$tid" "act_fold_failed"; return 1; }
    fi
  else
    if (( RANDOM % 5 == 0 )) && (( player_balance >= BIG_BLIND )); then
      pc "$act_dir" act --table-id "$tid" bet $BIG_BLIND || pc "$act_dir" act --table-id "$tid" check || true
    else
      pc "$act_dir" act --table-id "$tid" check || { print_state "$P1" "$tid" "act_check_failed"; return 1; }
    fi
  fi
  sleep 0.03
  return 0
}

nudge_progress(){
  local tid="$1" js cp act_dir
  js=$(state_json "$P1" "$tid" || true) || return 1
  cp=$(echo "$js" | jq -r '.current_player // ""')
  [[ -z "$cp" ]] && return 1
  act_dir=$(dir_for_id "$cp")
  [[ -z "$act_dir" ]] && return 1
  pc "$act_dir" act --table-id "$tid" fold >/dev/null 2>&1 || true
}

# Drive a hand with bounded actions, then **use events** to detect terminal state.
drive_hand_then_wait_terminal(){
  local tid="$1" max_actions="$2" actions=0 last_change=$SECONDS

  # Fast-drain any terminal event that already happened
  local ev; ev=$(wait_terminal_event "$P1" "$tid" "0s") || true
  if [[ -n "$ev" ]]; then printf '%s\n' "$ev"; return 0; fi

  while (( actions < max_actions )); do
    step_once "$tid" || true
    ((actions++))

    # Short poll for terminal outcome (fast path)
    ev=$(wait_terminal_event "$P1" "$tid" "150ms") || true
    if [[ -n "$ev" ]]; then
      printf '%s\n' "$ev"; return 0
    fi

    # Watchdog nudge if we stall
    if (( SECONDS - last_change >= STALL_WINDOW_SEC )); then
      nudge_progress "$tid"; last_change=$SECONDS
    fi
  done

  return 1
}

# ---------- Players & bankroll ----------

P1="$workdir/p1"; P2="$workdir/p2"; P3="$workdir/p3"; mkdir -p "$P1" "$P2" "$P3"

log "Seeding balances to $INITIAL_BANKROLL…"
set_balance "$P1" "$INITIAL_BANKROLL"
set_balance "$P2" "$INITIAL_BANKROLL"
set_balance "$P3" "$INITIAL_BANKROLL"

log "Creating 3-player table with buy-in $BUY_IN…"
TABLE_ID=$(pc "$P1" create-table --min-players 3 --max-players 3 --buy-in "$BUY_IN" --min-balance "$BUY_IN" --small-blind "$SMALL_BLIND" --big-blind "$BIG_BLIND" --starting-chips "$STARTING_CHIPS" --time-bank-seconds 1 --auto-start-ms "$AUTO_START_MS" | grep -E '^table_')
[[ -n "$TABLE_ID" ]] || die "failed to create table"
log "Table: $TABLE_ID"

pc "$P2" join --table-id "$TABLE_ID"
pc "$P3" join --table-id "$TABLE_ID"
pc "$P1" ready --table-id "$TABLE_ID" set
pc "$P2" ready --table-id "$TABLE_ID" set
pc "$P3" ready --table-id "$TABLE_ID" set

# Event-driven start
wait_game_started "$P1" "$TABLE_ID" || die "game did not start (event timeout)"

# Post-buy-in DCR balances stable
bal1_post=$(get_balance "$P1")
bal2_post=$(get_balance "$P2")
bal3_post=$(get_balance "$P3")
[[ "$bal1_post" -eq $((INITIAL_BANKROLL - BUY_IN)) ]] || die "P1 post-buyin balance mismatch: $bal1_post"
[[ "$bal2_post" -eq $((INITIAL_BANKROLL - BUY_IN)) ]] || die "P2 post-buyin balance mismatch: $bal2_post"
[[ "$bal3_post" -eq $((INITIAL_BANKROLL - BUY_IN)) ]] || die "P3 post-buyin balance mismatch: $bal3_post"

# ---------- Play hands ----------

log "Playing $HANDS_TO_PLAY hands with event-driven terminal detection…"
for (( hand=1; hand<=HANDS_TO_PLAY; hand++ )); do
  log "Hand #$hand"

  # Drive & wait for terminal (either SHOWDOWN_RESULT or NEW_HAND_STARTED)
  EVJSON=$(drive_hand_then_wait_terminal "$TABLE_ID" "$MAX_ACTIONS_PER_HAND") || {
    print_state "$P1" "$TABLE_ID" "hand_drive_timeout"
    die "hand did not reach a terminal state"
  }

  # If we have a showdown event, verify pot conservation
  # SHOWDOWN_RESULT payload shape: Notification with .showdown { winners[], pot }
  if echo "$EVJSON" | jq -e '.type=="SHOWDOWN_RESULT" or .Type=="SHOWDOWN_RESULT"' >/dev/null 2>&1; then
    pot=$(echo "$EVJSON" | jq -r '.showdown.pot // .Showdown.pot // 0' 2>/dev/null || echo 0)
    echo "EVENT: SHOWDOWN_RESULT pot=$pot"
    echo "$EVJSON" | jq '.' || echo "$EVJSON"

    # Cross-check with RPC GetLastWinners for conservation
    LW=$(winners_json "$P1" "$TABLE_ID")
    winners_count=$(echo "$LW" | jq -r '.winners | length')
    [[ "$winners_count" -ge 1 ]] || { echo "$LW" | jq '.'; die "no winners reported by GetLastWinners"; }
    sum_winnings=$(echo "$LW" | jq '[.winners[].winnings] | add // 0')
    if [[ "$sum_winnings" != "$pot" ]]; then
      printf '[%s] ERR: pot conservation failed: pot=%s sum_winnings=%s\n' "$(ts)" "$pot" "$sum_winnings" >&2
      echo "$LW" | jq '.'
      die "sum(winnings) != pot"
    fi
    log "Showdown verified: pot=$pot, winners_count=$winners_count"
  else
    # Non-showdown terminal (fold/new hand). Optional winner check.
    LW=$(winners_json "$P1" "$TABLE_ID")
    winners_count=$(echo "$LW" | jq -r '.winners | length' 2>/dev/null || echo 0)
    pot_rpc=$(echo "$LW" | jq -r '.pot // 0' 2>/dev/null || echo 0)
    if [[ "$winners_count" -ge 1 ]]; then
      log "Hand ended without showdown; winners_count=$winners_count pot=$pot_rpc"
    else
      log "Hand ended without showdown; RPC winners unavailable. Proceeding."
    fi
  fi
done

# Final DCR balance assertions (no off-table payouts expected)
final1=$(get_balance "$P1")
final2=$(get_balance "$P2")
final3=$(get_balance "$P3")

if [[ "$EXPECT_PAYOUT" == "true" ]]; then
  log "EXPECT_PAYOUT=true set, but payout expectations are not yet defined. Current balances: $final1, $final2, $final3"
else
  [[ "$final1" -eq "$bal1_post" ]] || die "P1 balance changed unexpectedly: $bal1_post -> $final1"
  [[ "$final2" -eq "$bal2_post" ]] || die "P2 balance changed unexpectedly: $bal2_post -> $final2"
  [[ "$final3" -eq "$bal3_post" ]] || die "P3 balance changed unexpectedly: $bal3_post -> $final3"
fi

log "Completed $HANDS_TO_PLAY hands. DCR balances verified."
echo "OK"
