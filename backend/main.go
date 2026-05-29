package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/gorilla/websocket"
)

type AppConfig struct {
	TvIP        string `json:"tv_ip"`
	AdbInstall  string `json:"adb_install"`
	Logcat      string `json:"logcat"`
	LogcatMedia string `json:"logcat_media"`
	LogcatHdmi  string `json:"logcat_hdmi"`
	LogcatFull  string `json:"logcat_full"`
	ShowToasts  string `json:"show_toasts"`
	Fallback    string `json:"fallback"`
	Dvforce     string `json:"dvforce"`
	Subtitles   string `json:"subtitles"`
	HdmiCheck   string `json:"hdmi_check"`
	HdmiCheckIp string `json:"hdmi_check_ip"`
	HideFolders string `json:"hide_folders"`
}

type FileItem struct {
	Name        string    `json:"name"`
	URL         string    `json:"url"`
	SubtitleURL string    `json:"subtitle_url,omitempty"`
	AudioURL    string    `json:"audio_url,omitempty"`
	Type        string    `json:"type"`
	HDRType     string    `json:"hdr_type"` 
	ModTime     time.Time `json:"-"`        
}

type WebOSMessage struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	URI     string          `json:"uri,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type SettingsPayload struct {
	Settings struct {
		PictureMode string `json:"pictureMode"`
	} `json:"settings"`
}

type LogBroadcaster struct {
	mu      sync.Mutex
	clients map[chan string]bool
	history []string
}

func NewLogBroadcaster() *LogBroadcaster {
	return &LogBroadcaster{
		clients: make(map[chan string]bool),
		history: make([]string, 0, 100),
	}
}

func (b *LogBroadcaster) Write(p []byte) (n int, err error) {
	msg := string(p)
	os.Stdout.Write(p) 
	
	b.mu.Lock()
	defer b.mu.Unlock()
	
	if len(b.history) >= 100 {
		b.history = b.history[1:]
	}
	b.history = append(b.history, msg)
	
	for ch := range b.clients {
		select {
		case ch <- msg:
		default:
		}
	}
	return len(p), nil
}

func (b *LogBroadcaster) Register(ch chan string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.clients[ch] = true
	for _, msg := range b.history {
		ch <- msg
	}
}

func (b *LogBroadcaster) Unregister(ch chan string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.clients, ch)
	close(ch)
}

var (
	mediaDir      = getEnv("MEDIA_DIR", "/media")
	cachedFiles   []byte
	runtimeMap    = make(map[string]string) 
	
	currentConfig  AppConfig
	cfgMutex       sync.RWMutex
	runtimeMutex   sync.RWMutex
	cacheMutex     sync.RWMutex
	rebuildTimer   *time.Timer
	rebuildMutex   sync.Mutex
	logBroadcaster = NewLogBroadcaster()
)

func loadConfig() {
	data, err := os.ReadFile("/ds-data/config.json")
	if err == nil {
		cfgMutex.Lock()
		json.Unmarshal(data, &currentConfig)
		cfgMutex.Unlock()
	} else {
		log.Println("⚠️ Could not read /ds-data/config.json, using fallback defaults")
	}
}

const indexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>DirectStreamer Web Dashboard</title>
    <style>
        body { font-family: sans-serif; background: #121212; color: #e0e0e0; margin: 0; padding: 20px; }
        .nav-menu { text-align: center; margin-bottom: 25px; font-size: 1.1em; }
        .nav-menu a { color: #007bff; text-decoration: none; margin: 0 15px; font-weight: bold; }
        .nav-menu a:hover { text-decoration: underline; color: #0056b3; }
        .nav-menu span { color: #555; }
        
        .content-section { max-width: 1150px; margin: 0 auto; display: none; }
        
        .file-item { background: #242424; margin-bottom: 8px; padding: 8px 12px; border-radius: 6px; display: flex; justify-content: space-between; align-items: center; box-shadow: 0 2px 4px rgba(0,0,0,0.3); }
        .file-name { font-size: 0.95em; font-weight: bold; color: #fff; word-break: break-all; }
        .hdr-tag { font-size: 0.7em; background: #444; padding: 2px 6px; border-radius: 12px; margin-left: 8px; color: #ddd; white-space: nowrap; }
        .actions { display: flex; gap: 8px; flex-shrink: 0; }
        
        button { border: none; padding: 8px 12px; border-radius: 4px; cursor: pointer; font-weight: bold; transition: opacity 0.2s; font-size: 0.85em; }
        button:hover { opacity: 0.8; }
        .btn-browser { background: #007bff; color: white; }
        .btn-tv { background: #28a745; color: white; }
        
        #play-url-section { text-align: center; background: #242424; padding: 40px 20px; border-radius: 8px; }
        #play-url-input { width: 80%; padding: 12px; margin-bottom: 20px; border-radius: 4px; border: 1px solid #444; background: #121212; color: white; font-size: 1em; }
        
        .form-group { margin-bottom: 15px; display: flex; justify-content: space-between; align-items: center; background: #242424; padding: 12px; border-radius: 6px; }
        .form-group label { color: #fff; font-weight: bold; flex: 1; }
        .form-group input, .form-group select { flex: 1; padding: 8px; border-radius: 4px; border: 1px solid #444; background: #121212; color: white; }
        
        #player-modal { display: none; position: fixed; top: 0; left: 0; width: 100%; height: 100%; background: rgba(0,0,0,0.95); z-index: 1000; align-items: center; justify-content: center; flex-direction: column; }
        #video-player { width: 90%; max-height: 80vh; outline: none; background: #000; }
        #close-modal { margin-top: 20px; background: #dc3545; color: white; font-size: 1em; padding: 8px 24px; }
        
        #log-console { background: #0a0a0a; color: #00ff00; font-family: monospace; height: 600px; overflow-y: auto; padding: 15px; border-radius: 6px; border: 1px solid #333; white-space: pre-wrap; font-size: 0.9em; box-shadow: inset 0 0 10px #000; }
    </style>
</head>
<body>
    <div class="nav-menu">
        <a href="#" onclick="switchTab('file-list')">filelist</a> <span>|</span>
        <a href="#" onclick="switchTab('play-url-section')">play url</a> <span>|</span>
        <a href="#" onclick="switchTab('settings-section')">settings</a> <span>|</span>
        <a href="#" onclick="switchTab('logs-section')">logs</a> <span>|</span>
        <a href="/download/DirectStreamer.apk">apk</a>
    </div>

    <div id="file-list" class="content-section">Loading files...</div>

    <div id="play-url-section" class="content-section">
        <input type="text" id="play-url-input" placeholder="Enter custom video URL (e.g., http://...)">
        <br>
        <button class="btn-browser" onclick="playInBrowser(document.getElementById('play-url-input').value)">▶ Browser</button>
        <button class="btn-tv" onclick="playOnTV(document.getElementById('play-url-input').value, null, null, null)">📺 TV</button>
    </div>

    <div id="settings-section" class="content-section">
        <form id="config-form" onsubmit="saveSettings(event)">
            <div class="form-group">
                <label>TV IP Address</label>
                <input type="text" id="cfg-tv-ip">
            </div>
            <div class="form-group">
                <label>Hide Folders (Comma-separated, e.g. Extras, Sample)</label>
                <input type="text" id="cfg-hide-folders" placeholder="Extras, Sample, Featurettes">
            </div>
            <div class="form-group">
                <label>ADB Auto-Install on Start</label>
                <select id="cfg-adb-install"><option value="yes">yes</option><option value="no">no</option></select>
            </div>
            <div class="form-group">
                <label>Logcat (App Only)</label>
                <select id="cfg-logcat"><option value="yes">yes</option><option value="no">no</option></select>
            </div>
            <div class="form-group">
                <label>Logcat (Media/Codecs)</label>
                <select id="cfg-logcat-media"><option value="yes">yes</option><option value="no">no</option></select>
            </div>
            <div class="form-group">
                <label>Logcat (HDMI Logs)</label>
                <select id="cfg-logcat-hdmi"><option value="yes">yes</option><option value="no">no</option></select>
            </div>
            <div class="form-group">
                <label>Show OSD Toasts in TV App</label>
                <select id="cfg-show-toasts"><option value="yes">yes</option><option value="no">no</option></select>
            </div>
            <div class="form-group">
                <label>Enable HDR10 Fallback for Dolby Vision dv7.06</label>
                <select id="cfg-fallback"><option value="yes">yes</option><option value="no">no</option></select>
            </div>
            <div class="form-group">
                <label>Force Amlogic Hardware Decoder (DV Profile 5/8)</label>
                <select id="cfg-dvforce"><option value="yes">yes</option><option value="no">no</option></select>
            </div>
            <div class="form-group">
                <label>Subtitles Default</label>
                <select id="cfg-subtitles"><option value="always">always</option><option value="off">off</option></select>
            </div>
            <div class="form-group">
                <label>Enable LG WebOS HDMI Check</label>
                <select id="cfg-hdmi-check"><option value="yes">yes</option><option value="no">no</option></select>
            </div>
            <div class="form-group">
                <label>LG WebOS TV IP (HDMI Check)</label>
                <input type="text" id="cfg-hdmi-check-ip">
            </div>
            <div style="text-align: right; margin-top: 15px;">
                <button class="btn-tv" type="submit">Save Configuration</button>
            </div>
        </form>
    </div>
    
    <div id="logs-section" class="content-section">
        <div id="log-console"></div>
    </div>

    <div id="player-modal">
        <video id="video-player" controls></video>
        <button id="close-modal">Close Player</button>
    </div>

    <script>
        let logEventSource = null;

        function switchTab(tabId) {
            document.querySelectorAll('.content-section').forEach(el => el.style.display = 'none');
            document.getElementById(tabId).style.display = 'block';
            if(tabId === 'settings-section') loadSettings();
        }

        function initLogs() {
            if (logEventSource) return;
            logEventSource = new EventSource('/api/logs');
            const consoleDiv = document.getElementById('log-console');
            logEventSource.onmessage = function(event) {
                consoleDiv.textContent += event.data + '\n';
                if (consoleDiv.textContent.length > 100000) {
                    consoleDiv.textContent = consoleDiv.textContent.slice(-50000);
                }
                const isNearBottom = consoleDiv.scrollHeight - consoleDiv.clientHeight <= consoleDiv.scrollTop + 50;
                if (isNearBottom) {
                    consoleDiv.scrollTop = consoleDiv.scrollHeight;
                }
            };
        }

        async function loadFiles() {
            try {
                const response = await fetch('/api/files');
                const files = await response.json();
                const list = document.getElementById('file-list');
                list.innerHTML = '';
                if (files.length === 0) { list.innerHTML = '<div style="text-align:center;">No media files found.</div>'; return; }
                files.forEach(file => {
                    const item = document.createElement('div');
                    item.className = 'file-item';
                    const subIcon = file.subtitle_url ? '<span class="hdr-tag" style="background:#007bff;">CC</span>' : '';
                    const audioIcon = file.audio_url ? '<span class="hdr-tag" style="background:#007bff;">External Audio</span>' : '';
                    const subArg = file.subtitle_url ? "'" + file.subtitle_url + "'" : "null";
                    const hdrArg = file.hdr_type ? "'" + file.hdr_type + "'" : "null";
                    const audioArg = file.audio_url ? "'" + file.audio_url + "'" : "null";
                    
                    item.innerHTML = 
                        '<div style="flex-grow:1; padding-right: 15px;"><div class="file-name">' + file.name + ' <span class="hdr-tag">' + file.hdr_type + '</span>' + subIcon + audioIcon + '</div></div>' +
                        '<div class="actions"><button class="btn-browser" onclick="playInBrowser(\'' + file.url + '\')">▶ Browser</button><button class="btn-tv" onclick="playOnTV(\'' + file.url + '\', ' + subArg + ', ' + hdrArg + ', ' + audioArg + ')">📺 TV</button></div>';
                    list.appendChild(item);
                });
            } catch (e) { document.getElementById('file-list').innerText = 'Failed to load media files.'; }
        }

        async function loadSettings() {
            const res = await fetch('/api/config');
            const data = await res.json();
            document.getElementById('cfg-tv-ip').value = data.tv_ip || '';
            document.getElementById('cfg-hide-folders').value = data.hide_folders || '';
            document.getElementById('cfg-adb-install').value = data.adb_install || 'no';
            document.getElementById('cfg-logcat').value = data.logcat || 'no';
            document.getElementById('cfg-logcat-media').value = data.logcat_media || 'no';
            document.getElementById('cfg-logcat-hdmi').value = data.logcat_hdmi || 'no';
            document.getElementById('cfg-show-toasts').value = data.show_toasts || 'yes';
            document.getElementById('cfg-fallback').value = data.fallback || 'yes';
            document.getElementById('cfg-dvforce').value = data.dvforce || 'yes';
            document.getElementById('cfg-subtitles').value = data.subtitles || 'always';
            document.getElementById('cfg-hdmi-check').value = data.hdmi_check || 'no';
            document.getElementById('cfg-hdmi-check-ip').value = data.hdmi_check_ip || '';
        }

        async function saveSettings(e) {
            e.preventDefault();
            const payload = {
                tv_ip: document.getElementById('cfg-tv-ip').value,
                hide_folders: document.getElementById('cfg-hide-folders').value,
                adb_install: document.getElementById('cfg-adb-install').value,
                logcat: document.getElementById('cfg-logcat').value,
                logcat_media: document.getElementById('cfg-logcat-media').value,
                logcat_hdmi: document.getElementById('cfg-logcat-hdmi').value,
                show_toasts: document.getElementById('cfg-show-toasts').value,
                fallback: document.getElementById('cfg-fallback').value,
                dvforce: document.getElementById('cfg-dvforce').value,
                subtitles: document.getElementById('cfg-subtitles').value,
                hdmi_check: document.getElementById('cfg-hdmi-check').value,
                hdmi_check_ip: document.getElementById('cfg-hdmi-check-ip').value
            };
            await fetch('/api/config', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(payload)
            });
            alert('Settings saved successfully! ADB Manager is restarting automatically.');
        }

        function playInBrowser(url) {
            if (!url) return;
            const modal = document.getElementById('player-modal');
            const player = document.getElementById('video-player');
            player.src = url;
            modal.style.display = 'flex';
            player.play();
        }

        async function playOnTV(url, subUrl, hdrType, audioUrl) {
            if (!url) return;
            try {
                let apiStr = '/api/play_on_tv?url=' + encodeURIComponent(url);
                if (subUrl) apiStr += '&sub=' + encodeURIComponent(subUrl);
                if (hdrType) apiStr += '&hdr=' + encodeURIComponent(hdrType);
                if (audioUrl) apiStr += '&audio=' + encodeURIComponent(audioUrl);
                const res = await fetch(apiStr);
                if (!res.ok) alert('Failed to send to TV. Ensure TV is on and the DirectStreamer app is open.');
            } catch(e) { alert('Network error while communicating with backend.'); }
        }

        document.getElementById('close-modal').addEventListener('click', () => {
            const modal = document.getElementById('player-modal');
            const player = document.getElementById('video-player');
            player.pause(); player.src = ''; modal.style.display = 'none';
        });

        initLogs();
        switchTab('file-list');
        loadFiles();
    </script>
</body>
</html>`

func getHDRType(filePath string) string {
	cmd := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_streams", filePath)
	var stdoutBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	if err := cmd.Run(); err != nil { return "Scanning Error" }
	probeOutput := strings.ToLower(stdoutBuf.String())

	if strings.Contains(probeOutput, "dovi") || strings.Contains(probeOutput, "dv_version") || strings.Contains(probeOutput, "dvh1") || strings.Contains(probeOutput, "dvc1") {
		profileVersion := ""
		for _, p := range []string{"5", "7", "8", "4", "9"} {
			if strings.Contains(probeOutput, "dv_profile="+p) || strings.Contains(probeOutput, "profile: "+p) || strings.Contains(probeOutput, "profile="+p) {
				profileVersion = " Profile " + p
				break
			}
		}
		return "◗◖ Dolby Vision" + profileVersion
	} else if strings.Contains(probeOutput, "smpte2084") || strings.Contains(probeOutput, "arib-std-b67") || strings.Contains(probeOutput, "bt2020") || strings.Contains(probeOutput, "smpte2094") {
		return "HDR10 / HLG"
	}
	return "SDR"
}

func isFolderHidden(folderName, hiddenListStr string) bool {
	if hiddenListStr == "" {
		return false
	}
	for _, h := range strings.Split(hiddenListStr, ",") {
		h = strings.TrimSpace(h)
		if h != "" && strings.EqualFold(folderName, h) {
			return true
		}
	}
	return false
}

func buildFileList() {
	hostIp := getEnv("BACKEND_IP", "127.0.0.1")
	hostPort := getEnv("PORT", "8282")
	var files []FileItem
	localMap := make(map[string]string)

	cfgMutex.RLock()
	hideFoldersStr := currentConfig.HideFolders
	cfgMutex.RUnlock()

	err := filepath.Walk(mediaDir, func(path string, info os.FileInfo, err error) error {
		if err != nil { return err }
		
		if info.IsDir() && path != mediaDir {
			if isFolderHidden(info.Name(), hideFoldersStr) {
				return filepath.SkipDir
			}
		}

		if !info.IsDir() {
			ext := strings.ToLower(filepath.Ext(info.Name()))
			if ext == ".mkv" || ext == ".mp4" {
				relPath, _ := filepath.Rel(mediaDir, path)
				cleanPath := filepath.ToSlash(relPath)
				u := url.URL{ Scheme: "http", Host: hostIp + ":" + hostPort, Path: "/stream/" + cleanPath }
				
				basePathNoExt := strings.TrimSuffix(path, filepath.Ext(path))
				
				srtPath := basePathNoExt + ".srt"
				var subURL string
				if _, err := os.Stat(srtPath); err == nil {
					subCleanPath := filepath.ToSlash(filepath.Join(filepath.Dir(relPath), filepath.Base(srtPath)))
					subU := url.URL{ Scheme: "http", Host: hostIp + ":" + hostPort, Path: "/stream/" + subCleanPath }
					subURL = subU.String()
				}
				
				var audioURL string
				// Check for complex extensions like .eac3.mka first!
				for _, aExt := range []string{".eac3.mka", ".ac3.mka", ".flac.mka", ".ac3", ".eac3", ".flac", ".mka", ".m4a"} {
				    if _, err := os.Stat(basePathNoExt + aExt); err == nil {
				        audioCleanPath := filepath.ToSlash(filepath.Join(filepath.Dir(relPath), filepath.Base(basePathNoExt + aExt)))
				        audioU := url.URL{ Scheme: "http", Host: hostIp + ":" + hostPort, Path: "/stream/" + audioCleanPath }
				        audioURL = audioU.String()
				        break
				    }
				}

				runtimeMutex.RLock()
				existingHdr, exists := runtimeMap[cleanPath]
				runtimeMutex.RUnlock()

				hdr := ""
				if exists && existingHdr != "Scanning Error" {
					hdr = existingHdr
				} else {
					hdr = getHDRType(path)
				}
				
				localMap[cleanPath] = hdr
				files = append(files, FileItem{ 
				    Name: info.Name(), 
				    URL: u.String(), 
				    SubtitleURL: subURL, 
				    AudioURL: audioURL,
				    Type: "video", 
				    HDRType: hdr, 
				    ModTime: info.ModTime(), 
				})
			}
		}
		return nil
	})

	if err == nil {
		sort.Slice(files, func(i, j int) bool { return files[i].ModTime.After(files[j].ModTime) })
		jsonBytes, _ := json.Marshal(files)
		cacheMutex.Lock()
		cachedFiles = jsonBytes
		cacheMutex.Unlock()
		runtimeMutex.Lock()
		runtimeMap = localMap
		runtimeMutex.Unlock()
		log.Println("✅ File list updated successfully.")
	} else {
		log.Printf("❌ Error scanning media directory: %v", err)
	}
}

func triggerRebuild() {
	rebuildMutex.Lock()
	defer rebuildMutex.Unlock()
	if rebuildTimer != nil { rebuildTimer.Stop() }
	rebuildTimer = time.AfterFunc(2 * time.Second, func() { buildFileList() })
}

func monitorMediaDirectory() {
	buildFileList()
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		for { time.Sleep(5 * time.Minute); buildFileList() }
		return
	}
	defer watcher.Close()

	addDirs := func(root string) {
		cfgMutex.RLock()
		hideFoldersStr := currentConfig.HideFolders
		cfgMutex.RUnlock()
		
		filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if info != nil && info.IsDir() { 
				if path != root {
					if isFolderHidden(info.Name(), hideFoldersStr) {
						return filepath.SkipDir
					}
				}
				watcher.Add(path) 
			}
			return nil
		})
	}
	addDirs(mediaDir)

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok { return }
			if event.Has(fsnotify.Create) || event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) || event.Has(fsnotify.Write) {
				info, err := os.Stat(event.Name)
				
				cfgMutex.RLock()
				hideFoldersStr := currentConfig.HideFolders
				cfgMutex.RUnlock()

				if err == nil && info.IsDir() { 
					if !isFolderHidden(info.Name(), hideFoldersStr) {
						addDirs(event.Name) 
					}
				}
				triggerRebuild()
			}
		case <-watcher.Errors:
		}
	}
}

func startHDMICheck() {
	for {
		cfgMutex.RLock()
		checkEnabled := currentConfig.HdmiCheck
		tvIP := currentConfig.HdmiCheckIp
		cfgMutex.RUnlock()

		if checkEnabled == "yes" && tvIP != "" {
			runLGWebOSMonitor(tvIP)
		}
		time.Sleep(10 * time.Second)
	}
}

func runLGWebOSMonitor(tvIP string) {
	keyFile := "/ds-data/.lgtv_key"
	u := url.URL{Scheme: "wss", Host: fmt.Sprintf("%s:3001", tvIP), Path: "/"}

	dialer := *websocket.DefaultDialer
	dialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	c, _, err := dialer.Dial(u.String(), nil)
	if err != nil { return }
	
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); c.Close() }()

	registerPayloadMap := map[string]interface{}{
		"forcePairing": false,
		"pairingType":  "PROMPT",
		"manifest": map[string]interface{}{ "manifestVersion": 1, "permissions": []string{"READ_SETTINGS"} },
	}
	if savedKey, err := os.ReadFile(keyFile); err == nil && len(savedKey) > 0 {
		registerPayloadMap["client-key"] = string(savedKey)
	}

	payloadBytes, _ := json.Marshal(registerPayloadMap)
	registerMsg := WebOSMessage{ Type: "register", ID: "register_0", Payload: payloadBytes }

	writeMutex := &sync.Mutex{}
	writeMutex.Lock()
	err = c.WriteJSON(registerMsg)
	writeMutex.Unlock()
	if err != nil { return }

	var lastFormat string
	for {
		var msg WebOSMessage
		if err := c.ReadJSON(&msg); err != nil { return }

		if msg.ID == "register_0" && msg.Type == "registered" {
			var responsePayload map[string]interface{}
			json.Unmarshal(msg.Payload, &responsePayload)
			if key, ok := responsePayload["client-key"].(string); ok {
				os.WriteFile(keyFile, []byte(key), 0644)
			}

			go func(ctx context.Context) {
				ticker := time.NewTicker(3 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done(): return
					case <-ticker.C:
						reqMsg := WebOSMessage{ Type: "request", ID: "req_settings", URI: "ssap://settings/getSystemSettings", Payload: json.RawMessage(`{"category": "picture", "keys": ["pictureMode"]}`) }
						writeMutex.Lock()
						err := c.WriteJSON(reqMsg)
						writeMutex.Unlock()
						if err != nil { return }
					}
				}
			}(ctx)
			
			firstReq := WebOSMessage{ Type: "request", ID: "req_settings", URI: "ssap://settings/getSystemSettings", Payload: json.RawMessage(`{"category": "picture", "keys": ["pictureMode"]}`) }
			writeMutex.Lock()
			c.WriteJSON(firstReq)
			writeMutex.Unlock()

		} else if msg.ID == "register_0" && msg.Type == "error" { return }

		if msg.ID == "req_settings" {
			var settingsPayload SettingsPayload
			if err := json.Unmarshal(msg.Payload, &settingsPayload); err == nil {
				rawMode := settingsPayload.Settings.PictureMode
				lowerMode := strings.ToLower(rawMode)
				var hdmiFormat string
				switch {
				case strings.HasPrefix(lowerMode, "dolby"): hdmiFormat = "Dolby Vision"
				case strings.HasPrefix(lowerMode, "hdr"): hdmiFormat = "HDR10"
				case rawMode != "": hdmiFormat = "SDR"
				default: hdmiFormat = "Unknown / No Signal"
				}

				if hdmiFormat != lastFormat && rawMode != "" {
					log.Printf("📺 LG OLED WebOS HDMI Format: %s (%s)", hdmiFormat, rawMode)
					lastFormat = hdmiFormat
				}
			}
		}
	}
}

type ThrottledReader struct {
	r           io.Reader
	bytesPerSec float64
	bucketSize  float64
	tokens      float64
	lastCheck   time.Time
	mu          sync.Mutex
}

func NewThrottledReader(r io.Reader, bytesPerSec int) *ThrottledReader {
	bps := float64(bytesPerSec)
	return &ThrottledReader{ r: r, bytesPerSec: bps, bucketSize: bps * 2, tokens: bps, lastCheck: time.Now() }
}

func (tr *ThrottledReader) Read(p []byte) (int, error) {
	tr.mu.Lock()
	now := time.Now()
	elapsed := now.Sub(tr.lastCheck).Seconds()
	tr.lastCheck = now

	tr.tokens += elapsed * tr.bytesPerSec
	if tr.tokens > tr.bucketSize { tr.tokens = tr.bucketSize }

	needed := float64(len(p))
	if tr.tokens < needed {
		if tr.tokens <= 0 {
			sleepTime := time.Duration((needed / tr.bytesPerSec) * float64(time.Second))
			tr.mu.Unlock()
			time.Sleep(sleepTime)
			return tr.Read(p)
		}
		needed = tr.tokens
	}
	tr.tokens -= needed
	tr.mu.Unlock()
	return tr.r.Read(p[:int(needed)])
}

func main() {
	log.SetOutput(logBroadcaster)
	log.SetFlags(log.Ldate | log.Ltime)

	loadConfig()
	port := getEnv("PORT", "8282")

	go monitorMediaDirectory()
	go startHDMICheck()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" { http.NotFound(w, r); return }
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(indexHTML))
	})

	http.HandleFunc("/api/logs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		ch := make(chan string, 100)
		logBroadcaster.Register(ch)
		defer logBroadcaster.Unregister(ch)

		fmt.Fprintf(w, "data: [DirectStreamer] Connected to server log stream...\n\n")
		flusher.Flush()

		for {
			select {
			case <-r.Context().Done():
				return
			case msg := <-ch:
				msg = strings.TrimSpace(msg)
				for _, line := range strings.Split(msg, "\n") {
					fmt.Fprintf(w, "data: %s\n", line)
				}
				fmt.Fprintf(w, "\n\n")
				flusher.Flush()
			}
		}
	})

	http.HandleFunc("/api/files", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "application/json")
		cacheMutex.RLock()
		if len(cachedFiles) > 0 {
			w.Write(cachedFiles)
		} else {
			w.Write([]byte("[]"))
		}
		cacheMutex.RUnlock()
	})

	http.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "application/json")
		
		if r.Method == http.MethodGet {
			cfgMutex.RLock()
			json.NewEncoder(w).Encode(currentConfig)
			cfgMutex.RUnlock()
		} else if r.Method == http.MethodPost {
			var newConfig AppConfig
			if err := json.NewDecoder(r.Body).Decode(&newConfig); err == nil {
				cfgMutex.Lock()
				currentConfig = newConfig
				
				triggerRebuild()
				
				tvIp := currentConfig.TvIP
				showToasts := currentConfig.ShowToasts
				fallback := currentConfig.Fallback
				dvforce := currentConfig.Dvforce
				subtitles := currentConfig.Subtitles
				
				cfgMutex.Unlock()
				
				data, _ := json.MarshalIndent(newConfig, "", "  ")
				os.WriteFile("/ds-data/config.json", data, 0644)
				
				go func() {
					log.Println("🔄 Settings saved. Restarting ADB Manager...")
					exec.Command("killall", "adb").Run()
					exec.Command("killall", "tail").Run()
				}()
				
				if tvIp != "" && tvIp != "0.0.0.0" {
					go func(ip, toasts, fb, dv, sub string) {
						client := &http.Client{Timeout: 3 * time.Second}
						reqUrl := fmt.Sprintf("http://%s:8080/config?show_toasts=%s&fallback=%s&dvforce=%s&subtitles=%s", ip, toasts, fb, dv, sub)
						client.Get(reqUrl)
					}(tvIp, showToasts, fallback, dvforce, subtitles)
				}
				
				w.Write([]byte(`{"status":"success"}`))
			} else {
				http.Error(w, "Bad JSON", http.StatusBadRequest)
			}
		}
	})

	http.HandleFunc("/api/play_on_tv", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		targetUrl := r.URL.Query().Get("url")
		subUrl := r.URL.Query().Get("sub")
		hdrType := r.URL.Query().Get("hdr")
		audioUrl := r.URL.Query().Get("audio")
		
		if targetUrl == "" { http.Error(w, "Missing URL", http.StatusBadRequest); return }
		
		cfgMutex.RLock()
		tvIP := currentConfig.TvIP
		cfgMutex.RUnlock()

		if tvIP == "" || tvIP == "0.0.0.0" { http.Error(w, "TV_IP not configured in settings", http.StatusInternalServerError); return }

		tvEndpoint := fmt.Sprintf("http://%s:8080/play?url=%s", tvIP, url.QueryEscape(targetUrl))
		if subUrl != "" {
		    tvEndpoint += fmt.Sprintf("&sub=%s", url.QueryEscape(subUrl))
		}
		if hdrType != "" {
		    tvEndpoint += fmt.Sprintf("&hdr=%s", url.QueryEscape(hdrType))
		}
		if audioUrl != "" {
		    tvEndpoint += fmt.Sprintf("&audio=%s", url.QueryEscape(audioUrl))
		}
		
		resp, err := http.Get(tvEndpoint)
		if err != nil { http.Error(w, "HTTP Error", http.StatusInternalServerError); return }
		defer resp.Body.Close()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success"}`))
	})

	http.HandleFunc("/stream/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Range, Content-Type")
		w.Header().Set("Access-Control-Expose-Headers", "Content-Range, Content-Length, Accept-Ranges")
		
		reqPath := strings.TrimPrefix(r.URL.Path, "/stream/")
		originalFile := filepath.Join(mediaDir, filepath.Clean(reqPath))
		
		file, err := os.Open(originalFile)
		if err != nil { http.Error(w, "File not found", http.StatusNotFound); return }
		defer file.Close()

		stat, err := file.Stat()
		if err != nil { http.Error(w, "Internal server error", http.StatusInternalServerError); return }

		rangeHeader := r.Header.Get("Range")
		if rangeHeader == "" || strings.HasPrefix(rangeHeader, "bytes=0-") {
			runtimeMutex.RLock()
			hdrType, exists := runtimeMap[reqPath]
			runtimeMutex.RUnlock()
			if !exists { hdrType = "Scanning..." }
			
			log.Printf("▶️ NOW PLAYING: %s | (%s)", reqPath, r.RemoteAddr)
			if !strings.HasSuffix(reqPath, ".srt") && !strings.HasSuffix(reqPath, ".ac3") && !strings.HasSuffix(reqPath, ".eac3") && !strings.HasSuffix(reqPath, ".flac") && !strings.HasSuffix(reqPath, ".mka") && !strings.HasSuffix(reqPath, ".eac3.mka") && !strings.HasSuffix(reqPath, ".ac3.mka") && !strings.HasSuffix(reqPath, ".flac.mka") && !strings.HasSuffix(reqPath, ".m4a") {
			    log.Printf("   [%s]", hdrType)
			}

			cfgMutex.RLock()
			fallbackEnabled := currentConfig.Fallback
			cfgMutex.RUnlock()
			
			if fallbackEnabled == "yes" && strings.Contains(hdrType, "Profile 7") {
				log.Printf("   ⚠️ [FALLBACK TRIGGERED] Dolby Vision Profile 7 detected. Instructing TV to fallback to HDR10.")
			}
		}

		w.Header().Set("Accept-Ranges", "bytes")
		
		if strings.HasSuffix(reqPath, ".srt") {
		    w.Header().Set("Content-Type", "application/x-subrip")
		} else if strings.HasSuffix(reqPath, ".ac3") {
		    w.Header().Set("Content-Type", "audio/ac3")
		} else if strings.HasSuffix(reqPath, ".eac3") {
		    w.Header().Set("Content-Type", "audio/eac3")
		} else if strings.HasSuffix(reqPath, ".flac") {
		    w.Header().Set("Content-Type", "audio/flac")
		} else if strings.HasSuffix(reqPath, ".mka") || strings.HasSuffix(reqPath, ".eac3.mka") || strings.HasSuffix(reqPath, ".ac3.mka") || strings.HasSuffix(reqPath, ".flac.mka") {
		    w.Header().Set("Content-Type", "audio/x-matroska")
		} else if strings.HasSuffix(reqPath, ".m4a") {
		    w.Header().Set("Content-Type", "audio/mp4")
		} else {
		    w.Header().Set("Content-Type", "video/x-matroska")
		}

		var start, end int64
		end = stat.Size() - 1

		if rangeHeader != "" {
			parts := strings.Split(strings.TrimPrefix(rangeHeader, "bytes="), "-")
			start, _ = strconv.ParseInt(parts[0], 10, 64)
			if len(parts) > 1 && parts[1] != "" { end, _ = strconv.ParseInt(parts[1], 10, 64) }
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, stat.Size()))
			w.WriteHeader(http.StatusPartialContent)
		} else {
			w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
			w.WriteHeader(http.StatusOK)
		}

		_, _ = file.Seek(start, io.SeekStart)
		limit := end - start + 1
		
		if strings.HasSuffix(reqPath, ".srt") || strings.HasSuffix(reqPath, ".ac3") || strings.HasSuffix(reqPath, ".eac3") || strings.HasSuffix(reqPath, ".flac") || strings.HasSuffix(reqPath, ".mka") || strings.HasSuffix(reqPath, ".eac3.mka") || strings.HasSuffix(reqPath, ".ac3.mka") || strings.HasSuffix(reqPath, ".flac.mka") || strings.HasSuffix(reqPath, ".m4a") {
		    io.Copy(w, io.LimitReader(file, limit))
		} else {
    		throttledFile := NewThrottledReader(io.LimitReader(file, limit), 50000000)
    		buf := make([]byte, 64*1024)
    		_, _ = io.CopyBuffer(w, throttledFile, buf)
		}
	})

	http.Handle("/download/", http.StripPrefix("/download/", http.FileServer(http.Dir("/app/public"))))

	log.Printf("Direct Play Backend server running on port %s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok { return value }
	return fallback
}
