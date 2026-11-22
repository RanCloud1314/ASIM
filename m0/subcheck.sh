#!/system/bin/sh
echo "== default data sub =="
settings get global multi_sim_data_call
echo "== sub1/sub2 data & roaming =="
for s in 1 2; do
  echo "-- sub $s --"
  dumpsys isub 2>/dev/null | grep -m1 "id=$s " | grep -oE "simSlotIndex=[0-9]+|isDataEnabled=[A-Za-z]+|[a-zA-Z]*[Rr]oaming[A-Za-z]*=[A-Za-z0-9-]+|mcc=[0-9]+|mnc=[0-9]+|displayName=[^,]*" | tr '\n' ' '; echo
done
echo "== service state per sub =="
dumpsys telephony.registry 2>/dev/null | grep "notifyServiceStateForSubscriber" | tail -2 | while read -r line; do
  echo "$(echo "$line" | grep -oE 'subId=[0-9]+' | head -1) $(echo "$line" | grep -oE 'mVoiceRegState=[0-9]+\([A-Z_]+\)') $(echo "$line" | grep -oE 'mDataRegState=[0-9]+\([A-Z_]+\)') op=$(echo "$line" | grep -oE 'mOperatorAlphaLong=[^,]*')"
done
echo "== rmnet_data1 owner =="
dumpsys connectivity 2>/dev/null | grep -B2 -A2 "rmnet_data1" | grep -iE "NetworkAgentInfo|SubId|owner|transports" | head -6 | cut -c1-200
