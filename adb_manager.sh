#!/bin/sh

TV_IP=$(jq -r '.tv_ip' /ds-data/config.json)
ADB_INSTALL=$(jq -r '.adb_install' /ds-data/config.json)
LOGCAT=$(jq -r '.logcat' /ds-data/config.json)
LOGCAT_MEDIA=$(jq -r '.logcat_media' /ds-data/config.json)
LOGCAT_HDMI=$(jq -r '.logcat_hdmi' /ds-data/config.json)
LOGCAT_FULL=$(jq -r '.logcat_full' /ds-data/config.json)

if [ -z "$TV_IP" ] || [ "$TV_IP" = "0.0.0.0" ]; then
    echo "ADB Manager: No TV_IP configured. Sleeping..."
    exec tail -f /dev/null
fi

if [ "$ADB_INSTALL" != "yes" ] && [ "$LOGCAT_FULL" != "yes" ] && [ "$LOGCAT_HDMI" != "yes" ] && [ "$LOGCAT_MEDIA" != "yes" ] && [ "$LOGCAT" != "yes" ]; then
    echo "ADB Manager: All ADB features disabled. Sleeping..."
    exec tail -f /dev/null
fi

echo "================================================================"
echo "⏳ Waiting for Backend Server to start..."
while ! wget -q --spider http://127.0.0.1:8282/; do
    sleep 1
done
echo "✅ Backend Server is up and running!"
echo "================================================================"

echo "Connecting to TV at ${TV_IP}:5555..."
sleep 1
adb connect ${TV_IP}:5555
echo "----------------------------------------------------------------"
echo "⏳ WAITING FOR AUTHORIZATION..."
echo "👉 If this is your first time, PLEASE LOOK AT YOUR TV SCREEN AND SELECT 'ALWAYS ALLOW' 👈"
echo "----------------------------------------------------------------"
adb -s ${TV_IP}:5555 wait-for-device
echo "✅ Device authorized successfully!"

if [ "$ADB_INSTALL" = "yes" ]; then
    echo "🗑️ Uninstalling existing app to prevent signature conflicts..."
    adb -s ${TV_IP}:5555 uninstall ninja.nwgat.directstreamer || true
    echo "📦 Installing DirectStreamer APK on the TV..."
    adb -s ${TV_IP}:5555 install -r -f -d /app/public/DirectStreamer.apk
    echo "🚀 Installation complete. Launching app on TV..."
    adb -s ${TV_IP}:5555 shell am start -n ninja.nwgat.directstreamer/.MainActivity
fi

if [ "$LOGCAT_FULL" = "yes" ]; then
    echo "📝 Full Logcat enabled. Streaming ALL device logs..."
    adb -s ${TV_IP}:5555 logcat -c
    exec adb -s ${TV_IP}:5555 logcat
elif [ "$LOGCAT_HDMI" = "yes" ] || [ "$LOGCAT_MEDIA" = "yes" ] || [ "$LOGCAT" = "yes" ]; then
    adb -s ${TV_IP}:5555 logcat -c
    REGEX=""
    [ "$LOGCAT_HDMI" = "yes" ] && REGEX="hdmitx"
    if [ "$LOGCAT_MEDIA" = "yes" ]; then
        [ -n "$REGEX" ] && REGEX="${REGEX}|mediacodec|omx|amlogic|dolby|ccodec|dvhe|c2decoder" || REGEX="mediacodec|omx|amlogic|dolby|ccodec|dvhe|c2decoder"
    fi
    if [ "$LOGCAT" = "yes" ]; then
        [ -n "$REGEX" ] && REGEX="${REGEX}|tvstream|ExoPlayer|DirectStreamer|AndroidRuntime" || REGEX="tvstream|ExoPlayer|DirectStreamer|AndroidRuntime"
    fi
    echo "📝 Streaming filtered logs: $REGEX"
    exec adb -s ${TV_IP}:5555 logcat | grep --line-buffered -iE "$REGEX"
else
    echo "✅ ADB tasks complete. Sleeping..."
    exec tail -f /dev/null
fi
