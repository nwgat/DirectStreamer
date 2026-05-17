# DirectStreamer (Server + Android TV App)

DirectStreamer addresses the common problem of unreliable Dolby Vision and HDR10 playback on Android TV. It consists of two parts:

*   **Backend Server:** Serves a list of the latest media files, sorted by date added.
*   **Android TV App:** A minimal client application for Android TV that handles playback control (Play/Pause).

The core principle is to minimize potential interference with HDR/DV signals. Our hypothesis (based on testing) is that on-screen elements – overlays, OSDs, and other visual enhancements – often cause issues.

**Features:**

*   **Prioritizes HDR/Dolby Vision:** Engineered for consistent, high-quality HDR/DV playback.
*   **Extremely Minimalist App:** The Android TV app includes only the essential "Play" and "Pause" controls.
*   **Recent Files Focused (Server):** The server serves a list of recently added files for quick access.
*   **Hardware Tested:** Xiaomi TV Box S 3rd Gen (Amlogic S905X5M)

**Important Considerations:**

*   **Not Feature-Rich:** DirectStreamer is *not* intended to be a full-featured media center experience. It's a focused solution for those prioritizing HDR/DV playback stability.
*   **Requires Both Components:** You need to deploy both the server and install the Android TV app for this to work.
