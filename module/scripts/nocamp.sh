#!/system/bin/sh
# ASIM nocamp.sh — keep the selected SIM slot(s) GSM-only so they never camp on
# LTE/NR in China, while the UICC stays powered for EAP-AKA (VoWiFi).
#
#   nocamp.sh lock <slot>     archive the user mask, then set GSM-only
#   nocamp.sh unlock <slot>   restore the archived mask
#   nocamp.sh enable <slot>   add the slot to SLOTS in asim.conf, then lock
#   nocamp.sh disable <slot>  remove it from SLOTS, then unlock
#   nocamp.sh slots           print the managed slot list
#   nocamp.sh status [slot]   human readable state
#   nocamp.sh selftest        pure-function checks + planned actions, no writes
#
# <slot> is the SIM slot id (simSlotIndex), NOT subId.

ASIM_DIR=${ASIM_DIR:-/data/adb/asim}
CONF=${CONF:-$ASIM_DIR/asim.conf}
TMPD=${TMPD:-${TMPDIR:-/data/local/tmp}}
GSM_ONLY_DEC=32768

log() { echo "nocamp: $*"; }
die() { echo "nocamp: ERROR: $*" >&2; return 1; }
isnum() { case $1 in ''|*[!0-9]*) return 1 ;; esac; }

dec2bin() {
  local n=$1 out=
  isnum "$n" || return 1
  while [ "$n" -gt 0 ]; do
    out=$((n % 2))$out
    n=$((n / 2))
  done
  echo "${out:-0}"
}

# _parse_mask <slot> — reads `dumpsys isub` on stdin, prints user mask (decimal).
_parse_mask() {
  local m
  # sed (not head) so the upstream dumpsys is read to EOF: no broken-pipe noise.
  m=$(grep -F "simSlotIndex=$1 " \
      | grep -o 'allowedNetworkTypesForReasons=[^ ]*' \
      | sed -n '1{s/.*user=//; s/,.*//; p}')
  isnum "$m" || return 1
  echo "$m"
}

slot_mask() {
  local m
  m=$(dumpsys isub | _parse_mask "$1") || return 1
  echo "$m"
}

# apply_mask <slot> <decimal> — the cmd argument is a binary string.
apply_mask() {
  local slot=$1 dec=$2 bin
  bin=$(dec2bin "$dec") || { die "bad mask [$dec]"; return 1; }
  cmd phone set-allowed-network-types-for-users -s "$slot" "$bin"
}

verify_mask() { # <slot> <decimal>
  local cur
  cur=$(slot_mask "$1") || return 1
  [ "$cur" = "$2" ] || { die "slot $1 mask is $cur, expected $2"; return 1; }
}

archive_of() { echo "$ASIM_DIR/slot$1.mask"; }

lock() {
  local slot=$1 f cur orig created=0
  isnum "$slot" || die "slot must be a number"
  f=$(archive_of "$slot")
  cur=$(slot_mask "$slot") || { die "cannot read mask for slot $slot"; return 1; }

  if [ "$cur" = "$GSM_ONLY_DEC" ]; then
    [ "${2:-}" = quiet ] || log "slot $slot already GSM-only"
    return 0
  fi

  if [ -f "$f" ]; then
    orig=$(cat "$f")
    isnum "$orig" || { die "bad archive $f"; return 1; }
  else
    mkdir -p "$ASIM_DIR" && chmod 700 "$ASIM_DIR" || return 1
    orig=$cur
    ( echo "$orig" > "$f" && chmod 600 "$f" ) || { rm -f "$f"; die "cannot write $f"; return 1; }
    created=1
  fi

  if ! apply_mask "$slot" "$GSM_ONLY_DEC" || ! verify_mask "$slot" "$GSM_ONLY_DEC"; then
    log "lock slot $slot failed, rolling back to $orig"
    apply_mask "$slot" "$orig" >/dev/null 2>&1
    # only drop the archive if this run created it; an older archive is the
    # last known good original and must survive a failed re-apply.
    [ "$created" -eq 1 ] && rm -f "$f"
    return 1
  fi
  log "slot $slot locked to GSM-only (original $orig archived)"
}

unlock() {
  local slot=$1 f orig
  isnum "$slot" || die "slot must be a number"
  f=$(archive_of "$slot")
  if [ ! -f "$f" ]; then
    [ "${2:-}" = quiet ] || log "slot $slot: no archive, nothing to restore"
    return 0
  fi
  orig=$(cat "$f")
  isnum "$orig" || { die "bad archive $f"; return 1; }

  if ! apply_mask "$slot" "$orig" || ! verify_mask "$slot" "$orig"; then
    die "restore slot $slot failed; archive kept at $f"
    return 1
  fi
  rm -f "$f"
  log "slot $slot restored to $orig"
}

# slots_edit <slot> on|off — rewrite SLOTS="..." in $CONF, print the new list.
slots_edit() {
  local slot=$1 want=$2 list out s tmp
  [ -f "$CONF" ] || { die "no config $CONF"; return 1; }
  list=$(sed -n 's/^SLOTS="\(.*\)"[[:space:]]*$/\1/p' "$CONF" | sed -n '$p')
  out=
  for s in $list; do
    [ "$s" = "$slot" ] || out="$out $s"
  done
  [ "$want" = on ] && out="$out $slot"
  out=$(echo $out)
  tmp=$CONF.tmp
  if sed "s/^SLOTS=.*/SLOTS=\"$out\"/" "$CONF" > "$tmp"; then
    mv "$tmp" "$CONF" && chmod 600 "$CONF" || return 1
  else
    rm -f "$tmp"
    return 1
  fi
  echo "$out"
}

# enable/disable <slot> — managed slot set + apply right now, so it survives a tick.
slot_set() {
  local slot=$1 want=$2 out
  out=$(slots_edit "$slot" "$want") || return 1
  log "SLOTS=\"$out\""
  if [ "$want" = on ]; then lock "$slot"; else unlock "$slot"; fi
}

slot_list() {  dumpsys isub | grep -o 'simSlotIndex=[0-9]*' | sed 's/.*=//' | sort -u
}

slot_line() { # <slot> — first SubscriptionInfoInternal record for that slot
  dumpsys isub | sed -n "/simSlotIndex=$1 /p" | sed -n '1p'
}

status() {
  local want=${1:-} s rec sub name mask arch locked
  for s in ${want:-$(slot_list)}; do
    rec=$(slot_line "$s")
    if [ -z "$rec" ]; then
      echo "slot $s: no subscription"
      continue
    fi
    sub=$(echo "$rec" | sed -n 's/.*SubscriptionInfoInternal: id=\([0-9]*\).*/\1/p')
    name=$(echo "$rec" | sed -n 's/.* displayName=\([^ ]*\).*/\1/p')
    mask=$(echo "$rec" | _parse_mask "$s") || mask=?
    arch=$(archive_of "$s")
    locked=no
    [ "$mask" = "$GSM_ONLY_DEC" ] && locked=yes
    echo "slot $s: subId=$sub name=$name mask=$mask locked=$locked orig=$([ -f "$arch" ] && cat "$arch" || echo -)"
  done
}

FAILS=0
check() { # <name> <expected> <actual>
  if [ "$2" = "$3" ]; then
    echo "ok   $1"
  else
    echo "FAIL $1: expected [$2] got [$3]"
    FAILS=$((FAILS + 1))
  fi
}
check_fails() { # <name> <command...> — command must fail
  if shift_cmd "$@"; then
    echo "FAIL $1: expected non-zero exit"
    FAILS=$((FAILS + 1))
  else
    echo "ok   $1"
  fi
}
shift_cmd() { shift; "$@" >/dev/null 2>&1; }

# Trimmed real `dumpsys isub` records (slots 1 and 0).
FIXTURE='  [SubscriptionInfoInternal: id=1 iccId=894430377[****] simSlotIndex=1 portIndex=0 carrierId=718 displayName=CTExcel carrierName=CHN-CT isOpportunistic=0 allowedNetworkTypesForReasons=user=54151 deviceToDeviceStatusSharingPreference=0]
  [SubscriptionInfoInternal: id=2 iccId=894416000[****] simSlotIndex=0 portIndex=0 carrierId=-1 displayName=Lebara carrierName=CHN-UNICOM isOpportunistic=0 allowedNetworkTypesForReasons=user=840583,power=54151 deviceToDeviceStatusSharingPreference=0]'

selftest() {
  echo "--- pure functions ---"
  check "dec2bin 840583" 11001101001110000111 "$(dec2bin 840583)"
  check "dec2bin 32768" 1000000000000000 "$(dec2bin 32768)"
  check "dec2bin 54151" 1101001110000111 "$(dec2bin 54151)"
  check "dec2bin 0" 0 "$(dec2bin 0)"

  echo "--- dumpsys parsing (fixture) ---"
  check "mask slot 1" 54151 "$(printf '%s\n' "$FIXTURE" | _parse_mask 1)"
  check "mask slot 0" 840583 "$(printf '%s\n' "$FIXTURE" | _parse_mask 0)"
  check_fails "mask missing slot" _parse_mask 5 < /dev/null

  echo "--- slots config editing (temp copy) ---"
  local td=$TMPD/asim-selftest
  rm -rf "$td" && mkdir -p "$td"
  printf 'SLOTS="1 2"\nROUTER_API="x"\n' > "$td/asim.conf"
  check "slots_edit off 1" "2" "$(CONF=$td/asim.conf slots_edit 1 off)"
  check "slots_edit on 1" "2 1" "$(CONF=$td/asim.conf slots_edit 1 on)"
  check "conf keeps other lines" 'ROUTER_API="x"' "$(sed -n '2p' "$td/asim.conf")"
  check "slots_edit off missing" "2 1" "$(CONF=$td/asim.conf slots_edit 7 off)"
  rm -rf "$td"

  echo "--- planned actions (read-only) ---"
  local s
  for s in $(slot_list); do
    if [ -f "$(archive_of "$s")" ]; then
      echo "plan slot $s: unlock -> $(cat "$(archive_of "$s")")"
    else
      echo "plan slot $s: lock -> $(dec2bin "$GSM_ONLY_DEC"), archive $(slot_mask "$s")"
    fi
  done

  [ "$FAILS" -eq 0 ] || { echo "selftest FAILED: $FAILS"; return 1; }
  echo "selftest passed"
}

main() {
  case "${1:-}" in
    lock) lock "${2:?slot}" ;;
    unlock) unlock "${2:?slot}" ;;
    enable) slot_set "${2:?slot}" on ;;
    disable) slot_set "${2:?slot}" off ;;
    slots) sed -n 's/^SLOTS="\(.*\)"$/\1/p' "$CONF" ;;
    status) status "${2:-}" ;;
    selftest) selftest ;;
    *) echo "usage: $0 lock|unlock|enable|disable|slots|status|selftest [slot]" >&2; exit 2 ;;
  esac
}

# Sourced by asimd.sh -> only define functions (never run main or exit).
if [ "${0##*/}" = "nocamp.sh" ]; then
  main "$@"
  exit $?
fi
