#!/system/bin/sh
# asimd — ASIM guard daemon. Keeps the configured slots GSM-only and writes
# state.json for the WebUI. Run via `asimd.sh start` from service.sh.
#
#   asimd.sh start|stop|status|run|selftest

ASIM_DIR=${ASIM_DIR:-/data/adb/asim}
CONF=$ASIM_DIR/asim.conf
LOG=$ASIM_DIR/asimd.log
PIDF=$ASIM_DIR/asimd.pid
LOCKD=$ASIM_DIR/asimd.lock.d
TMPD=${TMPDIR:-/data/local/tmp}
SELF=$(readlink -f "$0" 2>/dev/null || echo "$0")

SLOTS=""
ROUTER_API=""
ROUTER_TOKEN=""

# nocamp.sh defines lock/unlock/slot_mask/slot_line/archive_of/GSM_ONLY_DEC.
. "$(dirname "$SELF")/nocamp.sh"

# customize.sh only runs at install time; self-heal a wiped /data/adb for reboot paths.
if [ ! -f "$CONF" ] && [ "$(id -u)" = 0 ]; then
  mkdir -p "$ASIM_DIR" && chmod 700 "$ASIM_DIR"
  cp "$(dirname "$SELF")/../config/asim.conf" "$CONF" 2>/dev/null && chmod 600 "$CONF"
fi
[ -f "$CONF" ] && . "$CONF"

log() { echo "$(date '+%F %T') asimd: $*"; }

json_escape() { printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'; }
json_str() { # null for empty/unknown, quoted string otherwise
  if [ -z "$1" ] || [ "$1" = "-" ]; then echo null; else printf '"%s"' "$(json_escape "$1")"; fi
}

# reg_state <dumpsys telephony dump> <subId> — last registration snapshot,
# printed as "VOICE DATA IWLAN", "-" for each field it cannot find.
reg_state() {
  local line v d i
  line=$(sed -n "/notifyServiceStateForSubscriber: subId=$2 /p" "$1" | sed -n '$p')
  if [ -z "$line" ]; then
    echo "- - -"
    return 0
  fi
  v=$(echo "$line" | sed -n 's/.*mVoiceRegState=[0-9]*[(]\([A-Z_]*\)[)].*/\1/p')
  d=$(echo "$line" | sed -n 's/.*mDataRegState=[0-9]*[(]\([A-Z_]*\)[)].*/\1/p')
  i=$(echo "$line" | sed -n 's/.*transportType=WLAN registrationState=\([A-Z_]*\).*/\1/p')
  [ -n "$v" ] || v=-
  [ -n "$d" ] || d=-
  [ -n "$i" ] || i=-
  echo "$v $d $i"
}

compose_slot() { # <slot> <subId> <name> <mask> <orig> <managed> <voice> <data> <iwlan>
  local locked=false
  [ "$4" = "$GSM_ONLY_DEC" ] && locked=true
  printf '{"slot":%s,"subId":%s,"name":"%s","mask":%s,"orig":%s,"managed":%s,"locked":%s,"voice":%s,"data":%s,"iwlan":%s}' \
    "$1" "${2:-null}" "$(json_escape "$3")" "${4:-null}" "${5:-null}" \
    "$([ "$6" = true ] && echo true || echo false)" "$locked" \
    "$(json_str "$7")" "$(json_str "$8")" "$(json_str "$9")"
}

relay_sanitize() { # pass our own /status through; anything else becomes null
  case $1 in
    '{'*'"enabled"'*'}') printf '%s' "$1" ;;
    *) printf '{"enabled":null}' ;;
  esac
}

relay_status() {
  if [ -z "$ROUTER_API" ]; then
    echo '{"enabled":null}'
    return 0
  fi
  relay_sanitize "$(curl -s --max-time 3 -H "X-ASIM-Token: $ROUTER_TOKEN" \
    "$ROUTER_API/status" 2>/dev/null)"
}

write_state() {
  local tel=$TMPD/asimd-tel.$$ tmp=$ASIM_DIR/state.json.tmp
  local s m f rec sub name mask orig reg managed
  local first=1
  dumpsys telephony.registry > "$tel" 2>/dev/null || : > "$tel"
  {
    printf '{"updated":%s,"slots":[' "$(date +%s)"
    # every physical slot, so the WebUI can offer an unchecked one to take over
    for s in $(slot_list); do
      f=$(archive_of "$s")
      orig=""
      [ -f "$f" ] && orig=$(cat "$f")
      managed=false
      for m in $SLOTS; do [ "$m" = "$s" ] && managed=true; done
      rec=$(slot_line "$s")
      sub=$(echo "$rec" | sed -n 's/.*SubscriptionInfoInternal: id=\([0-9]*\).*/\1/p')
      name=$(echo "$rec" | sed -n 's/.* displayName=\([^ ]*\).*/\1/p')
      mask=$(slot_mask "$s")
      reg=$(reg_state "$tel" "${sub:-0}")
      set -- $reg
      [ "$first" -eq 1 ] || printf ','
      first=0
      compose_slot "$s" "${sub:-}" "${name:-unknown}" "$mask" "$orig" "$managed" "$1" "$2" "$3"
    done
    printf '],"relay":%s}\n' "$(relay_status)"
  } > "$tmp" && mv "$tmp" "$ASIM_DIR/state.json" && chmod 600 "$ASIM_DIR/state.json"
  rm -f "$tel"
}

tick() {
  local s out
  [ -f "$CONF" ] && . "$CONF"   # pick up SLOTS edits from the WebUI without a restart
  for s in $SLOTS; do
    case $s in
      *[!0-9]*) log "bad slot [$s] in SLOTS, skipped"; continue ;;
    esac
    out=$(lock "$s" quiet 2>&1) || log "slot $s lock failed: $out"
    [ -z "$out" ] || log "slot $s: $out"
  done
  write_state || log "write_state failed"
}

is_running() {
  local pid
  pid=$(cat "$PIDF" 2>/dev/null)
  [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null
}

# mkdir is atomic and, unlike toybox flock on Android mksh, survives exec.
# shortcut: a stale lock dir left by SIGKILL is reaped on the next start.
acquire_lock() {
  mkdir "$LOCKD" 2>/dev/null && return 0
  if is_running; then return 1; fi
  rm -rf "$LOCKD"
  mkdir "$LOCKD" 2>/dev/null
}

shutdown() {
  rm -rf "$LOCKD"
  rm -f "$PIDF"
  log "stopped"
}

run() {
  if ! acquire_lock; then
    log "another instance holds the lock, exit"
    return 0
  fi
  echo $$ > "$PIDF"
  # shortcut: no unlock from the TERM trap — `cmd phone` inside a signal trap of a
  # daemonized shell fails with FAILED_TRANSACTION on this device (verified), so
  # stop/uninstall.sh do the restore after this process is gone.
  trap 'shutdown; exit 0' TERM INT
  log "started (pid $$, slots: $SLOTS)"
  while :; do
    tick
    sleep 60 & wait $!
  done
}

start() {
  mkdir -p "$ASIM_DIR" && chmod 700 "$ASIM_DIR"
  if is_running; then
    log "already running (pid $(cat "$PIDF"))"
    return 0
  fi
  tick  # apply immediately instead of waiting for the first 60s tick
  nohup sh "$SELF" run >>"$LOG" 2>&1 </dev/null &
  sleep 2
  if is_running; then
    log "started (pid $(cat "$PIDF"))"
  else
    log "start failed, see $LOG"
    return 1
  fi
}

stop() {
  local pid f s i=0
  pid=$(cat "$PIDF" 2>/dev/null)
  if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
    kill "$pid" 2>/dev/null
    while [ $i -lt 15 ] && kill -0 "$pid" 2>/dev/null; do
      sleep 1
      i=$((i + 1))
    done
    kill -0 "$pid" 2>/dev/null && kill -9 "$pid" 2>/dev/null
  fi
  # unlock even when the daemon was already gone (crash, reboot, manual kill).
  # the archives are the truth: they are exactly the slots we changed.
  for f in "$ASIM_DIR"/slot*.mask; do
    [ -f "$f" ] || continue
    s=${f##*/slot}
    unlock "${s%.mask}"
  done
  rm -f "$PIDF"
  rm -rf "$LOCKD"
  log "stopped"
}

status() {
  if is_running; then
    echo "asimd: running (pid $(cat "$PIDF"))"
  else
    echo "asimd: stopped"
  fi
  echo "slots: ${SLOTS:-none}"
  echo "relay: $(relay_status)"
  [ -f "$ASIM_DIR/state.json" ] && echo "state: $ASIM_DIR/state.json"
}

FAILS=0
check() { # <name> <expected> <actual>
  if [ "$2" = "$3" ]; then
    echo "ok   $1"
  else
    echo "FAIL $1:"
    echo "  expected [$2]"
    echo "  actual   [$3]"
    FAILS=$((FAILS + 1))
  fi
}

TEL_FIXTURE=' 2026-11-22T16:02:23 - notifyServiceStateForSubscriber: subId=1 phoneId=1 state={mVoiceRegState=0(IN_SERVICE), mDataRegState=1(OUT_OF_SERVICE), mNetworkRegistrationInfos=[NetworkRegistrationInfo{ domain=PS transportType=WLAN registrationState=HOME}]}
 2026-11-22T16:02:23 - notifyServiceStateForSubscriber: subId=2 phoneId=0 state={mVoiceRegState=0(IN_SERVICE), mDataRegState=0(IN_SERVICE), mNetworkRegistrationInfos=[NetworkRegistrationInfo{ domain=PS transportType=WLAN registrationState=NOT_REG_OR_SEARCHING}]}'

selftest() {
  echo "--- json ---"
  check "escape quote/backslash" 'a\"b\\c' "$(json_escape 'a"b\c')"

  echo "--- reg parsing (fixture) ---"
  echo "$TEL_FIXTURE" > "$TMPD/asimd-tel-selftest"
  check "reg subId 1" "IN_SERVICE OUT_OF_SERVICE HOME" "$(reg_state "$TMPD/asimd-tel-selftest" 1)"
  check "reg subId 2" "IN_SERVICE IN_SERVICE NOT_REG_OR_SEARCHING" "$(reg_state "$TMPD/asimd-tel-selftest" 2)"
  check "reg missing subId" "- - -" "$(reg_state "$TMPD/asimd-tel-selftest" 9)"
  rm -f "$TMPD/asimd-tel-selftest"

  echo "--- slot object ---"
  check "managed + locked" \
    '{"slot":1,"subId":1,"name":"CTExcel","mask":32768,"orig":54151,"managed":true,"locked":true,"voice":"IN_SERVICE","data":"OUT_OF_SERVICE","iwlan":"HOME"}' \
    "$(compose_slot 1 1 CTExcel 32768 54151 true IN_SERVICE OUT_OF_SERVICE HOME)"
  check "not managed, no archive" \
    '{"slot":1,"subId":1,"name":"CTExcel","mask":54151,"orig":null,"managed":false,"locked":false,"voice":null,"data":null,"iwlan":null}' \
    "$(compose_slot 1 1 CTExcel 54151 "" false - - -)"

  echo "--- relay sanitize ---"
  check "relay ok" '{"enabled":true,"flows":[]}' "$(relay_sanitize '{"enabled":true,"flows":[]}')"
  check "relay garbage" '{"enabled":null}' "$(relay_sanitize 'Error 500')"
  check "relay empty" '{"enabled":null}' "$(relay_sanitize '')"

  [ "$FAILS" -eq 0 ] || { echo "selftest FAILED: $FAILS"; return 1; }
  echo "selftest passed"
}

main() {
  case "${1:-}" in
    start) start ;;
    stop) stop ;;
    status) status ;;
    state) write_state ;;
    run) run ;;
    selftest) selftest ;;
    *) echo "usage: $0 start|stop|status|state|run|selftest" >&2; exit 2 ;;
  esac
}

if [ "${0##*/}" = "asimd.sh" ]; then
  main "$@"
  exit $?
fi
