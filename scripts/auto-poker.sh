#!/usr/bin/env bash
set -Eeuo pipefail
IFS=$'\n\t'

###############################################################################
# Config (env-overridable)
###############################################################################
SESSION1="${SESSION1:-pokerclient_session1}"
SESSION2="${SESSION2:-pokerclient_session2}"
WIN_NAME="${WIN_NAME:-pokerclient}"

# Timeouts (seconds)
TO_MAIN_MENU=${TO_MAIN_MENU:-60}
TO_CREATE_RX=${TO_CREATE_RX:-5}
TO_JOIN_RX=${TO_JOIN_RX:-8}
TO_START_RX=${TO_START_RX:-60}
TO_AUTOPLAY=${TO_AUTOPLAY:-240}

###############################################################################
# UI markers / regexes
###############################################################################
MAIN_MENU_MARKER="List Tables|Create Table|Join Table|Check Balance|Quit"
CREATED_RX="Created table[[:space:]]*[:]?[[:space:]]*([A-Za-z0-9_.:-]+)"
JOINED_RX="Joined table[[:space:]]+([A-Za-z0-9_.:-]+)"
GAME_LOBBY_MARKER="Set Ready|Set Unready|Leave Table"
GAME_STARTED_RX="Game started!"
NEW_HAND_RX="New hand started!"
SHOWDOWN_RX="Showdown complete!"
ACTION_MARKER="Check|Call|Bet|Fold"
ONLY_LEAVE_RX="^[[:space:]]*Leave Table$"

###############################################################################
# Logging / errors
###############################################################################
ts(){ date '+%H:%M:%S'; }
log(){ printf '[%s] %s\n' "$(ts)" "$*"; }
warn(){ printf '[%s] [!] %s\n' "$(ts)" "$*" >&2; }
die(){ printf '[%s] ERR: %s\n' "$(ts)" "$*" >&2; exit 1; }

###############################################################################
# tmux helpers
###############################################################################
pane_for(){ # pane_for <session> <window-name>
  local s="$1" wname="$2" widx
  widx="$(tmux list-windows -t "$s" -F '#I #W' 2>/dev/null | awk -v n="$wname" '$2==n{print $1; exit}')"
  [[ -n "$widx" ]] || return 1
  echo "${s}:${widx}.0"
}

cap(){ # cap <target> [lines]
  local t="$1" lines="${2:-400}"
  tmux capture-pane -t "$t" -p -J -S "-$lines"
}

send(){ tmux send-keys -t "$1" "$2"; }
enter(){ tmux send-keys -t "$1" Enter; }
type_text(){ tmux send-keys -t "$1" -- "$2"; }

wait_for(){ # wait_for <target> <regex> [timeout]
  local t="$1" rx="$2" to="${3:-40}" start=$SECONDS
  while (( SECONDS - start < to )); do
    if cap "$t" 600 | grep -E -q "$rx"; then return 0; fi
    sleep 0.25
  done
  die "timeout waiting for /$rx/ in $t"
}

top_option(){ for _ in {1..10}; do send "$1" Up; done; }

safe_back_to_menu(){ # safe_back_to_menu <target>
  tmux send-keys -t "$1" q
  sleep 0.3
  wait_for "$1" "$MAIN_MENU_MARKER" 10
  top_option "$1"
}

ensure_running(){ # ensure_running <pane>
  local t="$1"
  if cap "$t" 300 | grep -E -q "$MAIN_MENU_MARKER|$GAME_LOBBY_MARKER|$GAME_STARTED_RX|$NEW_HAND_RX|Using client ID|Current balance"; then
    return 0
  fi
  send "$t" Up; enter "$t"
  wait_for "$t" "$MAIN_MENU_MARKER|$GAME_LOBBY_MARKER|$GAME_STARTED_RX|$NEW_HAND_RX|Using client ID|Current balance" 60 \
    || die "could not detect running client UI in $t"
}

###############################################################################
# Domain helpers
###############################################################################
extract_table_id(){ # extract_table_id <pane>
  cap "$1" 800 | sed -nE "s/.*$CREATED_RX.*/\1/p" | tail -n1 | tr -d '[:space:]'
}

is_turn(){ # is_turn <pane>
  # Most reliable cue on your client: "YOUR TURN - CHOOSE ACTION"
  # Also ensure we aren't in the "Leave Table" only state.
  local txt; txt="$(cap "$1" 160)"
  if grep -qE 'YOUR TURN[[:space:]]*- CHOOSE ACTION' <<<"$txt"; then
    if ! grep -qE "$ONLY_LEAVE_RX" <<<"$(printf "%s\n" "$txt" | sed -n '/Leave Table/p')"; then
      return 0
    fi
  fi
  return 1
}

create_table(){ # create_table <pane>
  local t="$1"
  top_option "$t"
  for downs in 1 2; do
    for _ in $(seq 1 "$downs"); do send "$t" Down; done
    enter "$t"     # open Create Table
    enter "$t"     # submit defaults
    if wait_for "$t" "$CREATED_RX" "$TO_CREATE_RX"; then
      local id; id="$(extract_table_id "$t")"
      [[ -n "$id" ]] || die "created table but failed to parse ID"
      echo "$id"
      return 0
    fi
    warn "Create attempt (downs=$downs) failed; backing out"
    safe_back_to_menu "$t"
  done
  die "failed to create table"
}

join_table(){ # join_table <pane> <table_id>
  local t="$1" table_id="$2"
  top_option "$t"
  for downs in 2 3; do
    for _ in $(seq 1 "$downs"); do send "$t" Down; done
    enter "$t"             # open Join form
    send "$t" C-u; sleep 0.1
    type_text "$t" "$table_id"; sleep 0.2; enter "$t"
    sleep 1
    # verify success / retry slow typing if needed
    if ! cap "$t" 80 | grep -q "$table_id"; then
      warn "Typed ID not detected; retrying slow type"
      send "$t" C-u; sleep 0.1
      for (( i=0; i<${#table_id}; i++ )); do
        type_text "$t" "${table_id:$i:1}"; sleep 0.05
      done
      sleep 0.2; enter "$t"
    fi
    if wait_for "$t" "$JOINED_RX" "$TO_JOIN_RX"; then return 0; fi
    warn "Join attempt (downs=$downs) failed; backing out"
    safe_back_to_menu "$t"
  done
  die "failed to join table"
}

choose_top_action(){ # ensure Call/Check
  local t="$1"
  for _ in {1..6}; do send "$t" Up; done
  enter "$t"
}

# --- Replace autoplay_one_hand with this version (ignores 2nd arg) ---
autoplay_one_hand(){ # autoplay_one_hand <pane> [_]
  local t="$1" start=$SECONDS
  local seen_hand=0

  while (( SECONDS - start < TO_AUTOPLAY )); do
    # Only look at a short recent tail to avoid stale lobby lines
    local buf="$(cap "$t" 70)"

    # Mark that the hand actually started
    if (( !seen_hand )) && grep -Eq "$GAME_STARTED_RX|$NEW_HAND_RX" <<<"$buf"; then
      seen_hand=1
    fi

    # End only on showdown (not lobby)
    if (( seen_hand )) && grep -Eq "$SHOWDOWN_RX" <<<"$buf"; then
      log "$t: showdown detected."
      # Optionally wait to see lobby again, but don't require it to finish
      wait_for "$t" "$GAME_LOBBY_MARKER" 30 || true
      return 0
    fi

    # Take action when it's our turn
    if is_turn "$t"; then
      choose_top_action "$t"     # Call/Check
      # If a bet amount prompt appears, back out and retry top action
      if cap "$t" 50 | grep -qiE 'enter (bet|raise) amount'; then
        send "$t" q; sleep 0.1; choose_top_action "$t"
      fi
      sleep 0.25
      continue
    fi

    sleep 0.25
  done
  warn "$t: autoplay timeout"
  return 1
}

###############################################################################
# Discover panes
###############################################################################
CLIENT1="$(pane_for "$SESSION1" "$WIN_NAME")" || {
  tmux list-windows -t "$SESSION1" -F '#S:#I #W' 2>/dev/null || true
  die "could not find window '$WIN_NAME' in session '$SESSION1'"
}
CLIENT2="$(pane_for "$SESSION2" "$WIN_NAME")" || {
  tmux list-windows -t "$SESSION2" -F '#S:#I #W' 2>/dev/null || true
  die "could not find window '$WIN_NAME' in session '$SESSION2'"
}
log "[*] Using CLIENT1=$CLIENT1"
log "[*] Using CLIENT2=$CLIENT2"

###############################################################################
# Preflight
###############################################################################
ensure_running "$CLIENT1"
ensure_running "$CLIENT2"
wait_for "$CLIENT1" "$MAIN_MENU_MARKER" "$TO_MAIN_MENU"
wait_for "$CLIENT2" "$MAIN_MENU_MARKER" "$TO_MAIN_MENU"

###############################################################################
# Step 1: Create table on CLIENT1
###############################################################################
log "[*] Creating table on $CLIENT1…"
TABLE_ID="$(create_table "$CLIENT1")"
log "[*] Table ID: $TABLE_ID"
# quick confirmation pass
if ! cap "$CLIENT1" 120 | grep -q "$TABLE_ID"; then
  warn "Table $TABLE_ID not visible yet in creator; re-extracting…"
  TABLE_ID="$(extract_table_id "$CLIENT1")"
  [[ -n "$TABLE_ID" ]] || die "Still failed to extract valid Table ID"
fi
sleep 2   # allow registration

###############################################################################
# Step 2: Join from CLIENT2
###############################################################################
log "[*] Joining from $CLIENT2…"
join_table "$CLIENT2" "$TABLE_ID"
log "[*] Joined."

###############################################################################
# Step 3: Both Set Ready
###############################################################################
wait_for "$CLIENT1" "$GAME_LOBBY_MARKER" 60
wait_for "$CLIENT2" "$GAME_LOBBY_MARKER" 60
top_option "$CLIENT1"; enter "$CLIENT1"
top_option "$CLIENT2"; enter "$CLIENT2"

###############################################################################
# Step 4: Wait for game start
###############################################################################
log "[*] Waiting for game to start…"
wait_for "$CLIENT1" "$GAME_STARTED_RX|$NEW_HAND_RX" "$TO_START_RX"
wait_for "$CLIENT2" "$GAME_STARTED_RX|$NEW_HAND_RX" "$TO_START_RX"
log "[*] Game started."

###############################################################################
# Step 5: Autoplay one hand
###############################################################################
log "[*] Autoplaying one hand…"
autoplay_one_hand "$CLIENT1" "$SHOWDOWN_RX|$GAME_LOBBY_MARKER" &
P1=$!
autoplay_one_hand "$CLIENT2" "$SHOWDOWN_RX|$GAME_LOBBY_MARKER" &
P2=$!
wait $P1 $P2 || true
log "[*] Done."
