#!/usr/bin/env bash
set -Eeuo pipefail
IFS=$'\n\t'

# Resolve repo root and bin dir
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

print_last_state(){
	set +e
	if [[ -n "${TABLE_ID:-}" ]] && declare -F pc >/dev/null 2>&1; then
		STATE=$(pc "$P1_DIR" state --table-id "$TABLE_ID" 2>/dev/null || true)
		JSON=$(printf '%s\n' "$STATE" | sed -n '/^{/,$p')
		if [[ -n "$JSON" ]]; then
			FINAL_JSON="$JSON"
			printf '%s\n' "$FINAL_JSON"
		fi
	fi
	set -e
}

die(){ printf '[%s] ERR: %s\n' "$(ts)" "$*" >&2; print_last_state; exit 1; }

command -v jq >/dev/null 2>&1 || die "jq is required"

# Build binaries
log "Building binaries…"
(cd "$REPO_ROOT" && go build -o "$BIN_DIR/pokerctl" ./cmd/pokerctl)
(cd "$REPO_ROOT" && go build -o "$BIN_DIR/pokersrv" ./cmd/pokersrv)

# Temp dirs/files
workdir=$(mktemp -d)
PORTFILE="$workdir/port"
DB="$workdir/poker.sqlite"
P1_DIR="$workdir/p1"
P2_DIR="$workdir/p2"
P3_DIR="$workdir/p3"
mkdir -p "$P1_DIR" "$P2_DIR" "$P3_DIR"

SRV_PID=""
cleanup(){
	# Print last known state if we aborted without success
	if [[ -n "${FINAL_JSON:-}" && "${OK_DONE:-false}" != true ]]; then
		printf '%s\n' "$FINAL_JSON" || true
	fi
	if [[ -n "$SRV_PID" ]]; then
		kill "$SRV_PID" 2>/dev/null || true
		wait "$SRV_PID" 2>/dev/null || true
	fi
	rm -rf "$workdir" || true
}
trap cleanup EXIT

# Start server
log "Starting server…"
"$BIN_DIR/pokersrv" -db "$DB" -host 127.0.0.1 -port 0 -portfile "$PORTFILE" &
SRV_PID=$!

# Wait for portfile
for i in {1..50}; do
	[[ -s "$PORTFILE" ]] && break
	sleep 0.1
done
[[ -s "$PORTFILE" ]] || die "server did not write portfile"
PORT=$(cat "$PORTFILE")
[[ -n "$PORT" ]] || die "empty port in portfile"

export GRPCHOST=127.0.0.1
export GRPCPORT="$PORT"

# pokerctl wrapper: pc <datadir> <args...>
pc(){ local dir="$1"; shift; "$BIN_DIR/pokerctl" -offline -grpcinsecure -grpchost "$GRPCHOST" -grpcport "$GRPCPORT" -datadir "$dir" "$@"; }

# Create table (3 players)
log "Creating 3p table…"
TABLE_ID=$(pc "$P1_DIR" create-table --min-players 3 --max-players 3 | grep -E '^table_')
[[ -n "$TABLE_ID" ]] || die "failed to obtain table id"
log "Table: $TABLE_ID"

# Join from P2 and P3
log "Player2 join…"
pc "$P2_DIR" join --table-id "$TABLE_ID"
log "Player3 join…"
pc "$P3_DIR" join --table-id "$TABLE_ID"

# Ready all
log "Setting all players ready…"
pc "$P1_DIR" ready --table-id "$TABLE_ID" set
pc "$P2_DIR" ready --table-id "$TABLE_ID" set
pc "$P3_DIR" ready --table-id "$TABLE_ID" set

# Autoplay concurrently
log "Autoplay one hand for all players…"
set +e
pc "$P1_DIR" autoplay-one-hand --table-id "$TABLE_ID" & A1=$!
pc "$P2_DIR" autoplay-one-hand --table-id "$TABLE_ID" & A2=$!
pc "$P3_DIR" autoplay-one-hand --table-id "$TABLE_ID" & A3=$!
wait $A1; r1=$?
wait $A2; r2=$?
wait $A3; r3=$?
rc=0
if (( r1 != 0 || r2 != 0 || r3 != 0 )); then rc=1; fi
set -e
[[ $rc -eq 0 ]] || die "autoplay exit $rc"

# Validate final state via polling with jq
log "Validating state…"
deadline=$((SECONDS+10))
OK_DONE=false
FINAL_JSON=""
while (( SECONDS < deadline )); do
	STATE=$(pc "$P1_DIR" state --table-id "$TABLE_ID" || true)
	JSON=$(printf '%s\n' "$STATE" | sed -n '/^{/,$p')
	if echo "$JSON" | jq -e '(
		.game_started == true and
		.players_joined == 3 and
		(.players | length) == 3 and
		((.phase_name == "SHOWDOWN") or (.phase == 6)) and
		(.pot // 0) > 0
	)' >/dev/null 2>&1; then
		FINAL_JSON="$JSON"
		OK_DONE=true
		break
	fi
	sleep 0.2
done

if [[ "$OK_DONE" != true ]]; then
	# Print last state and fail
	[[ -n "$JSON" ]] && printf '%s\n' "$JSON"
	die "validation failed"
fi

# Pretty-print final JSON and OK
echo "$FINAL_JSON" | jq .
echo "OK"
log "OK"
exit 0


