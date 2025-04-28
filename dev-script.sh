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
  'until nc -z localhost 19109; do echo waiting for dcrwallet; sleep 3; done;
   dcrlnd '"$NET"' --dcrd.rpchost=localhost --dcrd.rpcuser='"$RPCUSER"' \
          --dcrd.rpcpass='"$RPCPASS"' 2>&1 | tee '"$LOGDIR"'/dcrlnd.log'

# 2-bis : desbloqueio automático
tmux new-window -t "$SESSION":3 -n unlock \
  'until nc -z localhost 10009; do echo waiting for dcrlnd RPC; sleep 3; done;
   # tenta até conseguir; nao faz mal se já estiver desbloqueada
   until dcrlncli '"$NET"' getinfo >/dev/null 2>&1; do
        yes "'"$WALLETPASS"'" | dcrlncli '"$NET"' unlock || true
        sleep 2
   done;
   echo "dcrlnd unlocked !"'

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

###############################################################################
# 5-7 : brclient ×3  (já esperam brserver escutar em 12345)
###############################################################################
# BRCLIENT_SRC=~/projects/bisonrelay/brclient
# CLIENT_CFG_DIRS=(/home/vctt/brclientdirs/dir1 /home/vctt/brclientdirs/dir2 /home/vctt/brclientdirs/dir3)

# ( cd "$BRCLIENT_SRC" && go build -o brclient )

# for idx in "${!CLIENT_CFG_DIRS[@]}"; do
#   win=$((5 + idx))
#   cdir=${CLIENT_CFG_DIRS[$idx]}
#   cname="brclient$((idx+1))"
#   tmux new-window -t "$SESSION":$win -n "$cname" \
#     'until nc -z localhost 12345; do echo waiting for brserver; sleep 3; done;
#      cd '"$BRCLIENT_SRC"';
#      ./brclient -cfg '"$cdir"'/brclient.conf 2>&1 | tee '"$LOGDIR"'/'"$cname"'.log'
# done

tmux select-window -t "$SESSION":0
tmux attach -t "$SESSION"
