# DirectStreamer

DirectStreamer is a high-performance, containerized streaming solution designed for Android TV, focusing on direct-play playback with enhanced HDR/Dolby Vision support and automated deployment.

## ✨ Key Features

*   **Direct Play Architecture:** Minimal overhead backend serving media files directly to the client.
*   **Miminalistic Interface** Only show a list of files sorted by latest and a minimal player ui
*   **Enhanced HDR & Dolby Vision:** 
    *   Custom `AmlogicDolbyVisionCodecSelector` for optimized hardware decoding on Android TV.
    *   Enforced HDR fallback prevention to ensure consistent display formats.
*   **Automated Deployment:** Seamless "One-Click" build and install process using Docker and ADB (Android Debug Bridge).
*   **Smart Playback:**
    *   Throttled streaming to optimize network utilization.
    *   Smart Seek functionality for smooth navigation.
    *   Real-time subtitle and audio track cycling. (up dpad to cycle audio  track, down dpad to cycle subtitles, left/right dpad to seek)
*   **Developer-Friendly:**
*   *   Automatic detection of HDR10, HLG, and various Dolby Vision profiles via `ffprobe` to docker logs
    *   Built-in monitoring for LG OLED TVs to track HDMI input formats and signal changes in real-time to docker logs
    *   Integrated Logcat streaming directly to your Docker console.
    *   Persistent storage for TV authentication keys and configuration.


## Notes
* very much a work in progress
* *   * On my Xiaomi TV Box S (3rd Gen) dovi decoder is slightly buggy, on Dovi 7 content it switches between HDR10 or DV 7 randomly (i suspect it uses DV7 discarding to work)
* am not a coder, ive used Gemini AI, feel free to help improve it tho

## 🚀 Getting Started

todo
