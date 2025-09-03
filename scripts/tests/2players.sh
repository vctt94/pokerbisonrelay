#!/usr/bin/env bash
set -Eeuo pipefail
IFS=$'\n\t'

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# Find repo root by searching for go.mod upwards from the script directory
find_repo_root() {
    local d="$SCRIPT_DIR"
    while :; do
        if [ -f "$d/go.mod" ]; then
            echo "$d"
            return 0
        fi
        # Stop at filesystem root
        [ "$d" = "/" ] && break
        d="$(dirname "$d")"
    done
    # Fallback: two levels up from script dir (works for scripts/tests/*)
    (cd "$SCRIPT_DIR/../.." && pwd)
}
REPO_ROOT="$(find_repo_root)"
BIN_DIR="$REPO_ROOT/bin"
mkdir -p "$BIN_DIR"

build(){
  (cd "$REPO_ROOT" && go build -o "$BIN_DIR/pokerctl" ./cmd/pokerctl)
  (cd "$REPO_ROOT" && go build -o "$BIN_DIR/pokersrv" ./cmd/pokersrv)
}

log(){ printf '[%s] %s\n' "$(date +%H:%M:%S)" "$*"; }
die(){ echo "ERR: $*" >&2; exit 1; }

tmpdir=$(mktemp -d)
cleanup(){ rm -rf "$tmpdir" || true; }
trap cleanup EXIT

PORTFILE="$tmpdir/port"
DB="$tmpdir/poker.sqlite"

build

log "Starting server…"
"$BIN_DIR/pokersrv" -db "$DB" -host 127.0.0.1 -port 0 -portfile "$PORTFILE" &
SRV_PID=$!
sleep 0.3
PORT=$(cat "$PORTFILE")
[[ -n "$PORT" ]] || die "port not set"

export GRPCHOST=127.0.0.1
export GRPCPORT="$PORT"

# two temp datadirs
P1_DIR="$tmpdir/p1"
P2_DIR="$tmpdir/p2"
mkdir -p "$P1_DIR" "$P2_DIR"

pc(){ local dir="$1"; shift; "$BIN_DIR/pokerctl" -offline -grpcinsecure -grpchost "$GRPCHOST" -grpcport "$GRPCPORT" -datadir "$dir" "$@"; }

log "Creating table…"
TABLE_ID=$(pc "$P1_DIR" create-table | grep -E '^table_')
[[ -n "$TABLE_ID" ]] || die "no table id"
log "Table: $TABLE_ID"

log "P2 join…"
pc "$P2_DIR" join --table-id "$TABLE_ID"

log "Ready both…"
pc "$P1_DIR" ready --table-id "$TABLE_ID" set
pc "$P2_DIR" ready --table-id "$TABLE_ID" set

log "Autoplay…"
set +e
pc "$P1_DIR" autoplay-one-hand --table-id "$TABLE_ID" & P1=$!
pc "$P2_DIR" autoplay-one-hand --table-id "$TABLE_ID" & P2=$!
wait $P1 $P2
rc=$?
set -e
[[ $rc -eq 0 ]] || die "autoplay exit $rc"

log "Validate state…"
deadline=$((SECONDS+10))
ok=false
FINAL_JSON=""
while (( SECONDS < deadline )); do
  STATE=$(pc "$P1_DIR" state --table-id "$TABLE_ID" || true)
  JSON=$(printf '%s\n' "$STATE" | sed -n '/^{/,$p')
  if command -v jq >/dev/null 2>&1; then
    if echo "$JSON" | jq -e '(.game_started == true) or ((.pot // 0) > 0)' >/dev/null 2>&1; then
      FINAL_JSON="$JSON"; ok=true; break
    fi
  else
    lower=$(printf "%s" "$JSON" | tr '[:upper:]' '[:lower:]')
    if echo "$lower" | grep -q '"game_started": true'; then FINAL_JSON="$JSON"; ok=true; break; fi
    if echo "$lower" | grep -Eq '"pot":\s*[1-9]'; then FINAL_JSON="$JSON"; ok=true; break; fi
  fi
  sleep 0.2
done
[[ "$ok" == true ]] || { echo "$STATE"; die "final state validation failed"; }

# Always print the final JSON state on success
if command -v jq >/dev/null 2>&1; then
  echo "$FINAL_JSON" | jq .
else
  echo "$FINAL_JSON"
fi

log "OK"
kill $SRV_PID || true
wait $SRV_PID 2>/dev/null || true


