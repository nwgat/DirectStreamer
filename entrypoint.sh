#!/bin/sh
echo "Starting Backend Server in background..."
/app/server &
SERVER_PID=$!

# 1. Establish ADB Connection if either Install or Logcat is requested
if [ "$ADB_INSTALL" = "yes" ] || [ "$LOGCAT_FULL" = "yes" ] || [ "$LOGCAT_HDMI" = "yes" ] || [ "$LOGCAT_MEDIA" = "yes" ] || [ "$LOGCAT" = "yes" ]; then
    if [ -n "$TV_IP" ] && [ "$TV_IP" != "0.0.0.0" ]; then
        echo "================================================================"
        echo "Connecting to TV at ${TV_IP}:5555..."
        adb connect ${TV_IP}:5555
        
        echo "----------------------------------------------------------------"
        echo "⏳ WAITING FOR AUTHORIZATION..."
        echo "👉 If this is your/your TV's first time, PLEASE SELECT 'ALWAYS ALLOW' on the TV 👈"
        echo "----------------------------------------------------------------"
        adb -s ${TV_IP}:5555 wait-for-device
        echo "✅ Device authorized successfully!"
    fi
fi

# 2. Handle APK Installation (Only if ADB_INSTALL is yes)
if [ "$ADB_INSTALL" = "yes" ] && [ -n "$TV_IP" ] && [ "$TV_IP" != "0.0.0.0" ]; then
    echo "🗑️ Uninstalling existing app to prevent signature conflicts..."
    adb -s ${TV_IP}:5555 uninstall com.example.tvstream || true

    echo "📦 Installing DirectStreamer APK on the TV..."
    adb -s ${TV_IP}:5555 install -r -f -d /app/public/DirectStreamer.apk

    echo "🚀 Installation complete. Launching app on TV..."
    adb -s ${TV_IP}:5555 shell am start -n com.example.tvstream/.MainActivity
else
    echo "ADB Auto-Install disabled or TV IP not set. Skipping installation/launch."
fi

# 3. Handle Logcat Streaming (Independent of Installation)
if [ -n "$TV_IP" ] && [ "$TV_IP" != "0.0.0.0" ]; then
    if [ "$LOGCAT_FULL" = "logcat_full_check_dummy" ] || [ "$LOGCAT_FULL" = "yes" ]; then
        echo "📝 Full Logcat enabled. Streaming ALL device logs..."
        adb -s ${TV_IP}:5555 logcat -c
        adb -s ${TV_IP}:5555 logcat &
    elif [ "$LOGCAT_HDMI" = "yes" ]; then
        echo "📝 HDMI Logcat enabled. Streaming HDMI handshake logs..."
        adb -s ${TV_IP}:5555 logcat -c
        adb -s ${TV_IP}:5555 logcat | grep --line-buffered -i "hdmitx" &
    elif [ "$LOGCAT_MEDIA" = "yes" ]; then
        echo "📝 Media Logcat enabled. Streaming hardware codec logs..."
        adb -s ${TV_IP}:5555 logcat -c
        adb -s ${TV_IP}:5555 logcat -s MediaCodec OMXCodec AmlogicCodec DolbyVisionExtractor MediaCodecSource &
    elif [ "$LOGCAT" = "yes" ]; then
        echo "📝 App Logcat enabled. Streaming app logs..."
        adb -s ${TV_IP}:5555 logcat -c
        adb -s ${TV_IP}:5555 logcat | grep --line-buffered -iE "tvstream|ExoPlayer|DirectStreamer|AndroidRuntime" &
    fi
fi

wait $SERVER_PID
