#!/system/bin/sh
# wlan-domain state per phone, latest entries
dumpsys telephony.registry | grep "notifyServiceStateForSubscriber" | tail -4 | while read -r line; do
  sub=$(echo "$line" | grep -oE "subId=[0-9]+" | head -1)
  ph=$(echo "$line" | grep -oE "phoneId=[0-9]+" | head -1)
  wlan=$(echo "$line" | grep -oE "WLAN registrationState=[A-Z_]+" | head -1)
  wwan=$(echo "$line" | grep -oE "transportType=WWAN registrationState=[A-Z_]+" | head -1)
  echo "$sub $ph $wlan | $wwan"
done
echo "== ims reg state (radio) =="
logcat -d -b radio 2>/dev/null | grep "setImsRegistrationState" | tail -4
echo "== ims registration tech =="
dumpsys telephony.registry | grep -E "mImsRegistration|RegistrationTech" | head -6
