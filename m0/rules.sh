#!/system/bin/sh
# M0-B rule sync: idempotent — delete all pref 89xx, then add ePDG IPs -> table 8999 (asim0)
EPDG_IPS="31.94.76.2 31.94.76.5 31.94.76.6 31.94.76.9 31.94.76.10"

# cleanup: delete every rule in 8990-8999 range
for p in 8990 8991 8992 8993 8994 8995 8996 8997 8998 8999; do
  while ip rule del pref "$p" 2>/dev/null; do :; done
done
echo "after cleanup:"; ip rule | grep 899 || echo "  (none)"

# ensure route table
ip route replace default dev asim0 table 8999
ip route show table 8999

# add rules, one per pref, reporting each result
p=8999
for ip_ in $EPDG_IPS; do
  if ip rule add to "$ip_" pref "$p" table 8999 2>&1; then
    echo "OK $ip_ pref $p"
  else
    echo "FAIL $ip_ pref $p"
  fi
  p=$((p-1))
  sleep 1
done
echo "final:"; ip rule | grep 899
