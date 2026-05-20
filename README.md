# DirectStreamer

DirectStreamer is a high-performance, containerized streaming solution designed for Android TV, focusing on direct-play playback with enhanced HDR/Dolby Vision support and automated deployment.

## ✨ Key Features

*   **Direct Play Architecture:** Minimal overhead backend serving media files directly to the client.
*   **Miminalistic Interface**
    *   Only show a list of files sorted by latest
    *   Minimalistic Player UI
    *   Direct Play Backend     
*   **Enhanced HDR & Dolby Vision:** 
    *   Detect DoVi/HDR10 in media files and automaticly select correct decoder (bypasses amlogic)
    *   Disable Dovi 07.06 profiles and force them to HDR10
*   **Smart Playback:**
    *   Throttled streaming to optimize network utilization.
    *   Smart Seek functionality for smooth navigation. (, left/right dpad to seek)
    *   Real-time subtitle and audio track cycling. (up dpad to cycle audio  track, down dpad to cycle subtitles)
*   **Automated Deployment:** Seamless "One-Click" build and install process using Docker and ADB (Android Debug Bridge).
*   **Debug Monitoring:**
    *   Automatic detection of HDR10, HLG, and various Dolby Vision profiles via `ffprobe` in docker logs
    *   Built-in monitoring for LG OLED TVs to track HDMI input formats and signal changes in real-time in docker logs
    *   Built-in monitoring for Logcat directly to your Docker logs
    *   Persistent storage for TV/ADB authentication keys and configuration.

## Notes
* Very much a work in progress
* Mostly tested on my Xiaomi TV Box S (3rd Gen) aka Twilight 
* Am not a coder, ive used Gemini AI but feel free to help improve it tho, there is a reason why ive released it under GPLv3

## 🚀 Getting Started

todo

## ❤ Made with these Projects
Built with Alpine, Golang, Docker, ffmpeg, 
