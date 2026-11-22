#!/system/bin/sh
echo "== ipsec/iwlan interfaces =="
ip -o link 2>/dev/null | cut -d: -f2 | tr -d ' ' | grep -iE 'ipsec|iwlan|v4-|v6-|tun' || echo "(none)"
echo "== ip netns =="
ip netns list 2>&1 | head -5
echo "== distinct net namespaces =="
for p in $(ps -A -o PID 2>/dev/null | tail -n +2); do
  l=$(readlink /proc/$p/ns/net 2>/dev/null) || continue
  n=$(cat /proc/$p/comm 2>/dev/null)
  echo "$l $n"
done | sort -u | awk '{print $1, $2}' | sort | uniq -c -w12 | head -20
echo "== processes with own netns (non-init) =="
initns=$(readlink /proc/1/ns/net)
for p in $(ps -A -o PID 2>/dev/null | tail -n +2); do
  [ "$(readlink /proc/$p/ns/net 2>/dev/null)" = "$initns" ] && continue
  echo "$p $(cat /proc/$p/comm 2>/dev/null) $(readlink /proc/$p/ns/net 2>/dev/null)"
done | head -10
echo "== xfrm counts =="
echo "states: $(ip xfrm state 2>/dev/null | grep -c '^src')"
echo "policies: $(ip xfrm policy 2>/dev/null | grep -c '^src')"
echo "== wlan0 counters =="
cat /sys/class/net/wlan0/statistics/tx_packets 2>/dev/null
echo "== iwlan data network =="
dumpsys connectivity 2>/dev/null | grep -iE 'IWLAN' | head -8 | cut -c1-200
