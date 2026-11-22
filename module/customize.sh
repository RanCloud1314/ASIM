#!/system/bin/sh
# KernelSU 安装时执行（$MODPATH = 已解压的模块目录）。
MODPATH=${MODPATH:-$(dirname "$0")}

mkdir -p /data/adb/asim && chmod 700 /data/adb/asim

# 只在不存在时放默认配置：升级不覆盖用户改过的槽位/路由器地址。
if [ ! -f /data/adb/asim/asim.conf ]; then
  cp "$MODPATH/config/asim.conf" /data/adb/asim/asim.conf
  chmod 600 /data/adb/asim/asim.conf
fi
# 脚本一律用 `sh <file>` 调用，不依赖执行位（KernelSU 先跑 customize.sh 再解压其余文件）。
