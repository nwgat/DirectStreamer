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
* Mostly tested on my [Xiaomi TV Box S (3rd Gen) aka Twilight ]([url](https://www.androidtv-guide.com/streaming-gaming/xiaomi-tv-box-s-v3/))
* Am not a coder, ive used Gemini AI but feel free to help improve it tho, there is a reason why ive released it under GPLv3

## 🚀 Getting Started

on your android tv device
* Enable ADB debugging

on your server
* `apt-get install docker.io docker-compose-v2` (Ubuntu)
* `git clone https://github.com/nwgat/DirectStreamer && cd DirectStreamer`
* `nano .env`
* change `TV_IP=YourAndroidTV-IP`
* change `BACKEND_IP=to-your-server-ip`
* change `ADB_INSTALL=yes` to auto install on your android tv device


## todolist
- [ ] release the code
- [x] hope stability works out with high bitrate files
- [ ] Web Interface for backend with playback/browse
- [ ] fix hdr detection to include full profile names (dv07.06 etc) in docker logs
- [ ] some files show blackscreen on first play then correctly play on second
- [ ] audio transcoding on the backend to improve support on devices without certain codecs
- [ ] More Seeking options 5min/3min/30/15/10?
- [ ] OSD - seeking (0:23:41/1:30:34)
- [ ] OSD - convert toast to text overlay instead

## ❤ Made with these Projects
Built with Alpine, Golang, Docker, ffmpeg, android sdk 
