#!/bin/sh
RELEASE=${RELEASE:-yes}

# Apply optimizations: Build cache, no configuration cache, and disable daemon (ideal for Docker)
GRADLE_ARGS="--build-cache --no-configuration-cache --no-daemon --parallel"

echo "Running Gradle Build..."
if [ "$RELEASE" = "yes" ]; then
    echo "⚙️ Optimizing for Release Build..."
    gradle assembleRelease $GRADLE_ARGS
    
    # Standardize output for the final Docker copy command (ignoring whether signing is applied)
    cp app/build/outputs/apk/release/app-release-unsigned.apk app/build/outputs/apk/output.apk 2>/dev/null || \
    cp app/build/outputs/apk/release/app-release.apk app/build/outputs/apk/output.apk 2>/dev/null
else
    echo "🐛 Running Debug Build..."
    gradle assembleDebug $GRADLE_ARGS
    cp app/build/outputs/apk/debug/app-debug.apk app/build/outputs/apk/output.apk
fi
