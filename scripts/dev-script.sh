#!/usr/bin/env bash
set -Eeuo pipefail
SESSION=dcr_br_services
LOGDIR=/tmp/br_test ; mkdir -p "$LOGDIR"

NET="--testnet"
RPCUSER="rpcuser"
RPCPASS="rpcpass"
WALLETPASS="12345678"

tmux has-session -t "$SESSION" 2>/dev/null && tmux kill-session -t "$SESSION"

###############################################################################
# 0-2 : dcrd / dcrwallet / dcrlnd
###############################################################################
tmux new-session -d -s "$SESSION" -n dcrd \
  "dcrd $NET --rpcuser=$RPCUSER --rpcpass=$RPCPASS \
   2>&1 | tee $LOGDIR/dcrd.log"

tmux new-window -t "$SESSION":1 -n dcrwallet \
  'until nc -z localhost 19109; do echo waiting for dcrd; sleep 3; done;
   dcrwallet '"$NET"' --username='"$RPCUSER"' --password='"$RPCPASS"' \
   2>&1 | tee '"$LOGDIR"'/dcrwallet.log'

tmux new-window -t "$SESSION":2 -n dcrlnd \
  'until nc -z localhost 19110; do echo waiting for dcrwallet; sleep 3; done;
   dcrlnd '"$NET"' --dcrd.rpchost=localhost --dcrd.rpcuser='"$RPCUSER"' \
          --dcrd.rpcpass='"$RPCPASS"' 2>&1 | tee '"$LOGDIR"'/dcrlnd.log'

###############################################################################
# 4 : brserver  (usa porta 12345 por padrão)
###############################################################################
BRSERVER_DIR=~/projects/bisonrelay/brserver
tmux new-window -t "$SESSION":4 -n brserver \
  'until dcrlncli '"$NET"' getinfo >/dev/null 2>&1; do
        echo waiting for dcrlnd ready; sleep 3;
   done;
   cd '"$BRSERVER_DIR"';
   go build -o brserver;   # compila se precisar
   ./brserver             # ajuste flags se for usar outra porta
  '

tmux select-window -t "$SESSION":0
tmux attach -t "$SESSION"
