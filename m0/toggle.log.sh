#!/system/bin/sh
echo "== pid 3834 =="
tr '\0' ' ' < /proc/3834/cmdline 2>/dev/null; echo
echo "== iwlan packages =="
pm list packages 2>/dev/null | grep -iE 'iwlan|wfc'
echo "== wfc settings sub1 =="
dumpsys isub 2>/dev/null | grep -m1 'id=1' | tr ',' '\n' | grep -iE 'wfc|wifi'
echo "== cached state per airplane =="
settings get global airplane_mode_on
echo "== log 14:24:50..14:25:20 (non-adbd) =="
grep -aE '^11-22 14:2[45]:' /data/local/tmp/m0/live.log | grep -av 'adbd\|sensors\|Telephony( 3911):  isOnWfc\|SST-1' | grep -aiE 'iwlan|data|network|ims|modem|qmi|rat|radio' | tail -40 | cut -c1-190
