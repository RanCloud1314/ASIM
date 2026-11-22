#!/system/bin/sh
# KernelSU late_start service：等 telephony 就绪后再启动守护。
D=$(dirname "$0")

until [ "$(getprop sys.boot_completed)" = "1" ]; do
  sleep 2
done
sleep 20

exec sh "$D/scripts/asimd.sh" start
