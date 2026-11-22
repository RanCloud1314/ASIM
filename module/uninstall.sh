#!/system/bin/sh
# KernelSU 卸载时执行：先停守护（内含 unlock），再兜底恢复存档，最后删状态目录。
D=$(dirname "$0")

sh "$D/scripts/asimd.sh" stop

# 兜底：守护崩溃/从未启动时，存档还在就直接恢复，避免把卡留在 GSM-only。
for f in /data/adb/asim/slot*.mask; do
  [ -f "$f" ] || continue
  s=$(basename "$f" .mask)
  sh "$D/scripts/nocamp.sh" unlock "${s#slot}"
done

rm -rf /data/adb/asim
