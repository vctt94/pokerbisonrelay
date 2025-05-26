#!/usr/bin/env bash
set -Eeuo pipefail

###############################################################################
# Settings – change these if your layout differs
###############################################################################
SESSION=pokerbot_client               # tmux session name

# client
BRCLIENT_DIR=$HOME/projects/bisonrelay/brclient
CFG=/home/pokerbot/.brclient/brclient.conf
BRSERVER_PORT=12345
BR_RPC_PORT=7676

# bot
BOT_DIR=$HOME/projects/BR/poker-bisonrelay/cmd/bot

###############################################################################
# Restart session if it already exists
###############################################################################
tmux kill-session -t "$SESSION" 2>/dev/null || true

###############################################################################
# Window 0: brclient
###############################################################################
tmux new-session -d -s "$SESSION" -n brclient \
  "until nc -z localhost $BRSERVER_PORT; do \
       echo \"waiting for brserver on :$BRSERVER_PORT\"; sleep 3; \
   done; \
   cd $BRCLIENT_DIR && \
   go build -o brclient && \
   ./brclient --cfg $CFG"

###############################################################################
# Window 1: bot
###############################################################################
tmux new-window -t "$SESSION" -n bot bash -lc \
  "until nc -z localhost $BR_RPC_PORT; do \
       echo 'waiting for WS on :$BR_RPC_PORT'; sleep 3; \
   done; \
   cd $BOT_DIR && \
   go build -o bot && \
   ./bot; \
   exec bash"

###############################################################################
# Attach to window 0 by default (prefix+1 to switch to bot)
###############################################################################
tmux select-window -t "$SESSION":0
tmux attach-session -t "$SESSION"
