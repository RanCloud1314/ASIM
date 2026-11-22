#!/system/bin/sh
echo "udp6 sockets:"
cat /proc/net/udp6 2>/dev/null | tail -n +2 | while read -r line; do
  set -- $line
  echo "local=$2 remote=$3"
done | head -15
echo "== v6 rules =="
ip -6 rule 2>/dev/null | head -10
