#!/usr/bin/env bash
set -Eeuo pipefail

###############################################################################
# Discovery: two sessions (from your scripts) and the "pokerclient" window.
# Override via env: SESSION1, SESSION2, WIN_NAME
###############################################################################
SESSION1="${SESSION1:-pokerclient_session1}"
SESSION2="${SESSION2:-pokerclient_session2}"
WIN_NAME="${WIN_NAME:-pokerclient}"

pane_for() { # pane_for <session> <window-name>
  local s="$1" wname="$2"
  local widx
  widx="$(tmux list-windows -t "$s" -F '#I #W' 2>/dev/null | awk -v n="$wname" '$2==n{print $1; exit}')"
  [[ -n "$widx" ]] || return 1
  echo "${s}:${widx}.0"
}

CLIENT1="$(pane_for "$SESSION1" "$WIN_NAME")" || {
  echo "ERR: could not find window '$WIN_NAME' in session '$SESSION1'." >&2
  tmux list-windows -t "$SESSION1" -F '#S:#I #W' 2>/dev/null || true
  exit 1
}
CLIENT2="$(pane_for "$SESSION2" "$WIN_NAME")" || {
  echo "ERR: could not find window '$WIN_NAME' in session '$SESSION2'." >&2
  tmux list-windows -t "$SESSION2" -F '#S:#I #W' 2>/dev/null || true
  exit 1
}

echo "[*] Using CLIENT1=$CLIENT1"
echo "[*] Using CLIENT2=$CLIENT2"

###############################################################################
# Markers from your UI (strings in handleNotification + menus)
###############################################################################
MAIN_MENU_MARKER="List Tables|Create Table|Join Table|Check Balance|Quit"
CREATED_RX="Created table[[:space:]]+([A-Za-z0-9_.:-]+)"
JOINED_RX="Joined table[[:space:]]+([A-Za-z0-9_.:-]+)"
GAME_LOBBY_MARKER="Set Ready|Set Unready|Leave Table"
GAME_STARTED_RX="Game started!"
NEW_HAND_RX="New hand started!"
SHOWDOWN_RX="Showdown complete!"
ACTION_MARKER="Check|Call|Bet|Fold"
ONLY_LEAVE_RX="^[[:space:]]*Leave Table$"

###############################################################################
# tmux helpers
###############################################################################
cap(){ tmux capture-pane -t "$1" -p -J -S -400; }
send(){ tmux send-keys -t "$1" "$2"; }
enter(){ tmux send-keys -t "$1" Enter; }
type_text(){ tmux send-keys -t "$1" -- "$2"; }
die(){ echo "ERR: $*" >&2; exit 1; }

wait_for(){ # wait_for <target> <regex> [timeout]
  local t="$1" rx="$2" to="${3:-40}" start=$SECONDS
  while (( SECONDS - start < to )); do
    if cap "$t" | grep -E -q "$rx"; then return 0; fi
    sleep 0.25
  done
  die "timeout waiting for /$rx/ in $t"
}

top_option(){ for _ in {1..10}; do send "$1" Up; done; }

extract_table_id(){
  cap "$1" 800 | grep -E "$CREATED_RX" | tail -n1 | sed -nE "s/.*$CREATED_RX.*/\1/p"
}

is_turn(){
  local txt; txt="$(cap "$1" 120)"
  if grep -E -q "$ACTION_MARKER" <<<"$txt"; then
    if ! grep -E -q "$ONLY_LEAVE_RX" <<<"$(printf "%s\n" "$txt" | sed -n '/Leave Table/p')"; then
      return 0
    fi
  fi
  return 1
}

ensure_running(){ # ensure_running <pane>
  local t="$1"
  # Already looks like UI?
  if cap "$t" | grep -E -q "$MAIN_MENU_MARKER|$GAME_LOBBY_MARKER|$GAME_STARTED_RX|$NEW_HAND_RX|Using client ID|Current balance"; then
    return 0
  fi
  # Try rerun previous command (your pane shows "↑ to rerun")
  send "$t" Up; enter "$t"
  wait_for "$t" "$MAIN_MENU_MARKER|$GAME_LOBBY_MARKER|$GAME_STARTED_RX|$NEW_HAND_RX|Using client ID|Current balance" 60 \
    || die "could not detect running client UI in $t"
}

###############################################################################
# Preflight: start both UIs WITHOUT sending 'q'
###############################################################################
ensure_running "$CLIENT1"
ensure_running "$CLIENT2"

# Require main menu before proceeding (we won't send 'q' here to avoid quitting)
wait_for "$CLIENT1" "$MAIN_MENU_MARKER" 60
wait_for "$CLIENT2" "$MAIN_MENU_MARKER" 60

###############################################################################
# Step 1: CLIENT1 creates table with defaults.
# Strategy:
#  - go to first menu item (top_option)
#  - try "Down + Enter + Enter" (Create Table if no "Return to Table")
#  - if not created, we back out safely (we're in a subview) with 'q'
#  - then try "Down Down + Enter + Enter" (Create Table if "Return to Table" exists)
###############################################################################
echo "[*] Creating table on $CLIENT1…"
top_option "$CLIENT1"

try_create(){
  local t="$1" downs="$2" # 1 or 2 downs depending on presence of "Return to Table"
  for _ in $(seq 1 "$downs"); do send "$t" Down; done
  enter "$t"      # enter Create Table form
  enter "$t"      # submit defaults
  # Wait briefly to see if table was created
  if wait_for "$t" "$CREATED_RX" 5; then return 0; fi
  # If we didn't create, we're in some subview; 'q' here is SAFE (doesn't quit app)
  tmux send-keys -t "$t" q
  # Give UI a moment to return to main menu
  sleep 0.3
  # Ensure main menu again
  wait_for "$t" "$MAIN_MENU_MARKER" 10 || return 1
  top_option "$t"
  return 1
}

if ! try_create "$CLIENT1" 1; then
  # Try the 2-down variant
  if ! try_create "$CLIENT1" 2; then
    die "failed to create table from $CLIENT1"
  fi
fi

TABLE_ID="$(extract_table_id "$CLIENT1")"
[[ -n "$TABLE_ID" ]] || die "Failed to extract Table ID from CLIENT1 screen"
echo "[*] Table ID: $TABLE_ID"

###############################################################################
# Step 2: CLIENT2 joins by typing the ID
###############################################################################
echo "[*] Joining from $CLIENT2…"
top_option "$CLIENT2"
# Navigate to "Join Table" (Down twice if no "Return to Table"; thrice if it exists).
# We'll try both patterns robustly.
try_join(){
  local t="$1" downs="$2"
  for _ in $(seq 1 "$downs"); do send "$t" Down; done
  enter "$t"                # open join form
  type_text "$t" "$TABLE_ID"
  enter "$t"
  if wait_for "$t" "$JOINED_RX" 6; then return 0; fi
  # back to main menu (safe)
  tmux send-keys -t "$t" q
  sleep 0.3
  wait_for "$t" "$MAIN_MENU_MARKER" 10 || return 1
  top_option "$t"
  return 1
}

# Without "Return to Table": index = 2 downs; with it: 3 downs.
if ! try_join "$CLIENT2" 2; then
  if ! try_join "$CLIENT2" 3; then
    die "failed to join table from $CLIENT2"
  fi
fi
echo "[*] Joined."

###############################################################################
# Step 3: Both Set Ready (first option in lobby)
###############################################################################
wait_for "$CLIENT1" "$GAME_LOBBY_MARKER" 60
wait_for "$CLIENT2" "$GAME_LOBBY_MARKER" 60
top_option "$CLIENT1"; enter "$CLIENT1"
top_option "$CLIENT2"; enter "$CLIENT2"

###############################################################################
# Step 4: Wait for game to start
###############################################################################
echo "[*] Waiting for game to start…"
wait_for "$CLIENT1" "$GAME_STARTED_RX|$NEW_HAND_RX" 60
wait_for "$CLIENT2" "$GAME_STARTED_RX|$NEW_HAND_RX" 60
echo "[*] Game started."

###############################################################################
# Step 5: Autoplay a single hand (Enter when it's our turn => Check/Call)
###############################################################################
autoplay(){
  local t="$1" end_rx="$2" start=$SECONDS
  while (( SECONDS - start < 240 )); do
    if cap "$t" | grep -E -q "$end_rx"; then
      echo "[*] $t: hand over."
      return 0
    fi
    if is_turn "$t"; then
      enter "$t"
      sleep 0.15
    fi
    sleep 0.25
  done
  echo "[!] $t: autoplay timeout" >&2
  return 1
}

echo "[*] Autoplaying one hand…"
autoplay "$CLIENT1" "$SHOWDOWN_RX|$GAME_LOBBY_MARKER" &
P1=$!
autoplay "$CLIENT2" "$SHOWDOWN_RX|$GAME_LOBBY_MARKER" &
P2=$!
wait $P1 $P2 || true
echo "[*] Done."
