#!/system/bin/sh
# M0 diagnostic: IMS/WFC/IWLAN current state, one line per fact.
echo "== phone1 data connections =="
dumpsys telephony.registry | grep "notifyDataConnectionForSubscriber: phoneId=1" | tail -2 | cut -c1-160
echo "== ims reg =="
dumpsys telephony.registry | grep -i "mImsRegistration" | head -2
dumpsys telephony.registry | grep -i "notifyImsRegistration" | tail -2 | cut -c1-160
echo "== wlan reg =="
dumpsys telephony.registry | grep "PS transportType=WLAN" | tail -1 | grep -oE "WLAN registrationState=[A-Z_]+"
echo "== xfrm =="
ip xfrm state 2>/dev/null | grep "^src" | head -4
echo "== recent iwlan/ims logs =="
logcat -d 2>/dev/null | grep -iE "IkeSession|EpdgSelector|IwlanNetwork|IWlanProxy" | tail -6 | cut -c1-160
