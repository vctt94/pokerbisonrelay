#!/usr/bin/env bash
set -Eeuo pipefail

###############################################################################
# Settings
###############################################################################
SESSION=pokerclient_session1           # tmux session name

# bison-relay client
BRCLIENT_DIR=$HOME/projects/bisonrelay/brclient
CFG=$HOME/brclientdirs/dir1/brclient.conf
BRSERVER_PORT=12345                    # relays TCP port
BR_RPC_PORT=7777                       # client’s WS RPC port

# poker client
POKERCLIENT_DIR=$HOME/projects/BR/pokerbisonrelay/cmd/client
POKER_DATADIR=$HOME/pokerclientdirs/dir1

###############################################################################
# Restart session if it already exists
###############################################################################
tmux kill-session -t "$SESSION" 2>/dev/null || true

###############################################################################
# Window 0 – brclient
###############################################################################
tmux new-session -d -s "$SESSION" -n brclient "
until nc -z localhost $BRSERVER_PORT; do
    echo 'waiting for brserver on :$BRSERVER_PORT'; sleep 3
done
cd \"$BRCLIENT_DIR\"
go build -o brclient
./brclient --cfg \"$CFG\"
"

###############################################################################
# Window 1 – poker client (interactive shell, pane stays open)
###############################################################################
tmux new-window  -t "$SESSION":1 -n pokerclient "$SHELL"

tmux send-keys  -t "$SESSION":1 "
until nc -z localhost $BR_RPC_PORT; do
    echo 'waiting for WS on :$BR_RPC_PORT'; sleep 3
done
cd \"$POKERCLIENT_DIR\"
echo '--- poker client running (Ctrl-C to stop, ↑ to rerun) ---'
go build -o client && ./client --datadir \"$POKER_DATADIR\"
" C-m

###############################################################################
# Start attached on window 0 (Prefix-2 to jump to poker client)
###############################################################################
tmux select-window -t "$SESSION":0
tmux attach-session -t "$SESSION"
