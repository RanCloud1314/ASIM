#!/system/bin/sh
# relay.sh — 手机侧访问路由器 asim-relay 的唯一入口。
#
# WebUI 不能直接 fetch 路由器：KSU 的 WebView 是 https 源，访问明文 LAN 会被
# 混合内容拦掉，所以统一走这里由 root shell 发 curl。
#
#   relay.sh router                 当前路由器地址（auto/saved）
#   relay.sh setrouter <url|auto>   保存路由器地址，auto=跟随默认网关
#   relay.sh status                 中继状态
#   relay.sh get                    节点配置
#   relay.sh put <b64>              保存节点配置（JSON 的 base64）
#   relay.sh ping [b64]             测节点延迟（可选未保存的节点 JSON）

ASIM_DIR=${ASIM_DIR:-/data/adb/asim}
CONF=$ASIM_DIR/asim.conf
TMPD=${TMPDIR:-/data/local/tmp}
[ -f "$CONF" ] && . "$CONF"

# normalize_router: 接受 192.168.1.1 / 192.168.1.1:8080 / http://host，补全协议和端口。
normalize_router() {
  local u=${1%/} host
  [ -n "$u" ] || return 1
  case $u in
    *[\"\'\`\\\;\ \&\|\<\>]*) return 1 ;;
  esac
  case $u in
    http://*|https://*) ;;
    *) u="http://$u" ;;
  esac
  host=${u#*://}
  case $host in
    *:*) ;;
    *) u="$u:8080" ;;
  esac
  printf '%s' "$u"
}

# router_url: ROUTER_API 为空时跟随手机默认网关（PC 部署时用户自己填 PC 地址）。
router_url() {
  if [ -n "$ROUTER_API" ]; then
    printf '%s' "${ROUTER_API%/}"
    return 0
  fi
  local gw
  gw=$(ip -4 route 2>/dev/null | sed -n 's/^default via \([0-9.]*\).*/\1/p' | head -n1)
  [ -n "$gw" ] || gw=$(ip -4 route get 8.8.8.8 2>/dev/null | sed -n 's/.* via \([0-9.]*\).*/\1/p' | head -n1)
  [ -n "$gw" ] || return 1
  printf 'http://%s:8080' "$gw"
}

setrouter() {
  local u=""
  if [ -n "$1" ] && [ "$1" != auto ]; then
    u=$(normalize_router "$1") || return 2
  fi
  if grep -q '^ROUTER_API=' "$CONF" 2>/dev/null; then
    sed -i "s|^ROUTER_API=.*|ROUTER_API=\"$u\"|" "$CONF" || return 3
  else
    printf '\nROUTER_API="%s"\n' "$u" >> "$CONF" || return 3
  fi
  ROUTER_API=$u
}

json_error() { printf '{"error":"%s"}' "$1"; }

# fetch <GET|POST|PUT> <path> [body-file] [timeout]
fetch() {
  local base out rc t=${4:-15}
  base=$(router_url) || { json_error no_router; return 3; }
  if [ -n "$3" ]; then
    out=$(curl -s --max-time "$t" -X "$1" -H "X-ASIM-Token: $ROUTER_TOKEN" \
      --data-binary @"$3" "$base$2")
  else
    out=$(curl -s --max-time "$t" -X "$1" -H "X-ASIM-Token: $ROUTER_TOKEN" "$base$2")
  fi
  rc=$?
  if [ $rc -ne 0 ] || [ -z "$out" ]; then
    json_error "unreachable (curl $rc)"
    return 4
  fi
  printf '%s' "$out"
}

# bodyfile <b64> → 打印临时文件路径
bodyfile() {
  local f=$TMPD/asim-relay-body.$$
  printf '%s' "$1" | base64 -d > "$f" 2>/dev/null || { rm -f "$f"; return 1; }
  printf '%s' "$f"
}

main() {
  local f
  case "${1:-}" in
    router)
      f=$(router_url) || { json_error no_router; return 3; }
      printf '{"url":"%s","saved":"%s"}\n' "$f" "${ROUTER_API%/}"
      ;;
    setrouter)
      setrouter "${2:-}" || { json_error bad_url; return 2; }
      f=$(router_url) || f=""
      printf '{"url":"%s","saved":"%s"}\n' "$f" "${ROUTER_API%/}"
      ;;
    status) fetch GET /status ;;
    enable|disable) fetch POST "/$1" ;;
    get)    fetch GET /config ;;
    put)
      f=$(bodyfile "${2:-}") || { json_error bad_body; return 2; }
      fetch PUT /config "$f" 15
      rm -f "$f"
      ;;
    ping)
      if [ -n "${2:-}" ]; then
        f=$(bodyfile "$2") || { json_error bad_body; return 2; }
        fetch POST /ping "$f" 25
        rm -f "$f"
      else
        fetch POST /ping "" 25
      fi
      ;;
    *) echo "usage: $0 router|setrouter|status|enable|disable|get|put|ping" >&2; exit 2 ;;
  esac
}

[ "${0##*/}" = "relay.sh" ] && main "$@"
