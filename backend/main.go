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

type FileItem struct {
	Name    string    `json:"name"`
	URL     string    `json:"url"`
	Type    string    `json:"type"`
	HDRType string    `json:"hdr_type"` 
	ModTime time.Time `json:"-"`        
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

var (
	mediaDir      = getEnv("MEDIA_DIR", "/media")
	cachedFiles   []byte
	runtimeMap    = make(map[string]string) 
	runtimeMutex  sync.RWMutex
	cacheMutex    sync.RWMutex
	rebuildTimer  *time.Timer
	rebuildMutex  sync.Mutex
)

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
        
        .content-section { max-width: 960px; margin: 0 auto; }
        
        .file-item { background: #242424; margin-bottom: 8px; padding: 8px 12px; border-radius: 6px; display: flex; justify-content: space-between; align-items: center; box-shadow: 0 2px 4px rgba(0,0,0,0.3); }
        .file-name { font-size: 0.95em; font-weight: bold; color: #fff; word-break: break-all; }
        .hdr-tag { font-size: 0.7em; background: #444; padding: 2px 6px; border-radius: 12px; margin-left: 8px; color: #ddd; white-space: nowrap; }
        .actions { display: flex; gap: 8px; flex-shrink: 0; }
        
        button { border: none; padding: 8px 12px; border-radius: 4px; cursor: pointer; font-weight: bold; transition: opacity 0.2s; font-size: 0.85em; }
        button:hover { opacity: 0.8; }
        .btn-browser { background: #007bff; color: white; }
        .btn-tv { background: #28a745; color: white; }
        
        #play-url-section { display: none; text-align: center; background: #242424; padding: 40px 20px; border-radius: 8px; }
        #play-url-input { width: 80%; padding: 12px; margin-bottom: 20px; border-radius: 4px; border: 1px solid #444; background: #121212; color: white; font-size: 1em; }
        
        #player-modal { display: none; position: fixed; top: 0; left: 0; width: 100%; height: 100%; background: rgba(0,0,0,0.95); z-index: 1000; align-items: center; justify-content: center; flex-direction: column; }
        #video-player { width: 90%; max-height: 80vh; outline: none; background: #000; }
        #close-modal { margin-top: 20px; background: #dc3545; color: white; font-size: 1em; padding: 8px 24px; }
    </style>
</head>
<body>
    <div class="nav-menu">
        <a href="#" onclick="switchTab('file-list')">filelist</a> <span>|</span>
        <a href="#" onclick="switchTab('play-url-section')">play url</a> <span>|</span>
        <a href="/download/DirectStreamer.apk">apk</a>
    </div>

    <div id="file-list" class="content-section">Loading files...</div>

    <div id="play-url-section" class="content-section">
        <input type="text" id="play-url-input" placeholder="Enter custom video URL (e.g., http://...)">
        <br>
        <button class="btn-browser" onclick="playInBrowser(document.getElementById('play-url-input').value)">▶ Browser</button>
        <button class="btn-tv" onclick="playOnTV(document.getElementById('play-url-input').value)">📺 TV</button>
    </div>

    <div id="player-modal">
        <video id="video-player" controls></video>
        <button id="close-modal">Close Player</button>
    </div>

    <script>
        function switchTab(tabId) {
            document.getElementById('file-list').style.display = 'none';
            document.getElementById('play-url-section').style.display = 'none';
            document.getElementById(tabId).style.display = 'block';
        }

        async function loadFiles() {
            try {
                const response = await fetch('/api/files');
                const files = await response.json();
                const list = document.getElementById('file-list');
                list.innerHTML = '';
                
                if (files.length === 0) {
                    list.innerHTML = '<div style="text-align:center;">No media files found.</div>';
                    return;
                }

                files.forEach(file => {
                    const item = document.createElement('div');
                    item.className = 'file-item';
                    item.innerHTML = 
                        '<div style="flex-grow:1; padding-right: 15px;">' +
                            '<div class="file-name">' + file.name + ' <span class="hdr-tag">' + file.hdr_type + '</span></div>' +
                        '</div>' +
                        '<div class="actions">' +
                            '<button class="btn-browser" onclick="playInBrowser(\'' + file.url + '\')">▶ Browser</button>' +
                            '<button class="btn-tv" onclick="playOnTV(\'' + file.url + '\')">📺 TV</button>' +
                        '</div>';
                    list.appendChild(item);
                });
            } catch (e) {
                document.getElementById('file-list').innerText = 'Failed to load media files.';
            }
        }

        function playInBrowser(url) {
            if (!url) return;
            const modal = document.getElementById('player-modal');
            const player = document.getElementById('video-player');
            player.src = url;
            modal.style.display = 'flex';
            player.play();
        }

        async function playOnTV(url) {
            if (!url) return;
            try {
                const res = await fetch('/api/play_on_tv?url=' + encodeURIComponent(url));
                if (!res.ok) {
                    alert('Failed to send to TV. Ensure TV is on and ADB is connected.');
                }
            } catch(e) {
                alert('Network error while communicating with backend.');
            }
        }

        document.getElementById('close-modal').addEventListener('click', () => {
            const modal = document.getElementById('player-modal');
            const player = document.getElementById('video-player');
            player.pause();
            player.src = '';
            modal.style.display = 'none';
        });

        // Initialize display
        switchTab('file-list');
        loadFiles();
    </script>
</body>
</html>`

func getHDRType(filePath string) string {
	cmd := exec.Command("ffprobe", 
		"-v", "error", 
		"-select_streams", "v:0", 
		"-show_streams", 
		filePath,
	)
	
	var stdoutBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	
	if err := cmd.Run(); err != nil {
		return "Scanning Error"
	}

	probeOutput := strings.ToLower(stdoutBuf.String())

	if strings.Contains(probeOutput, "dovi") || 
	   strings.Contains(probeOutput, "dv_version") || 
	   strings.Contains(probeOutput, "dvh1") || 
	   strings.Contains(probeOutput, "dvc1") {
		
		profileVersion := ""
		for _, p := range []string{"5", "7", "8", "4", "9"} {
			if strings.Contains(probeOutput, "dv_profile="+p) || 
			   strings.Contains(probeOutput, "profile: "+p) ||
			   strings.Contains(probeOutput, "profile="+p) {
				profileVersion = " Profile " + p
				break
			}
		}
		return "Dolby Vision" + profileVersion
	} else if strings.Contains(probeOutput, "smpte2084") || 
	          strings.Contains(probeOutput, "arib-std-b67") || 
	          strings.Contains(probeOutput, "bt2020") ||
	          strings.Contains(probeOutput, "smpte2094") {
		return "HDR10 / HLG"
	}

	return "SDR"
}

func buildFileList() {
	hostIp := getEnv("BACKEND_IP", "127.0.0.1")
	hostPort := getEnv("PORT", "8282")

	var files []FileItem
	localMap := make(map[string]string)

	err := filepath.Walk(mediaDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			ext := strings.ToLower(filepath.Ext(info.Name()))
			if ext == ".mkv" || ext == ".mp4" {
				relPath, _ := filepath.Rel(mediaDir, path)
				cleanPath := filepath.ToSlash(relPath)
				
				u := url.URL{
					Scheme: "http",
					Host:   hostIp + ":" + hostPort,
					Path:   "/stream/" + cleanPath,
				}
				
				runtimeMutex.RLock()
				existingHdr, exists := runtimeMap[cleanPath]
				runtimeMutex.RUnlock()

				var hdr string
				// Use cache to prevent running ffprobe on unchanged files
				if exists && existingHdr != "Scanning Error" {
					hdr = existingHdr
				} else {
					hdr = getHDRType(path)
				}
				
				localMap[cleanPath] = hdr
				
				files = append(files, FileItem{
					Name:    info.Name(),
					URL:     u.String(),
					Type:    "video",
					HDRType: hdr,
					ModTime: info.ModTime(),
				})
			}
		}
		return nil
	})

	if err == nil {
		sort.Slice(files, func(i, j int) bool {
			return files[i].ModTime.After(files[j].ModTime)
		})

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
	if rebuildTimer != nil {
		rebuildTimer.Stop()
	}
	// Debounce for 2 seconds to avoid spanning multiple events during file copies
	rebuildTimer = time.AfterFunc(2 * time.Second, func() {
		buildFileList()
	})
}

func monitorMediaDirectory() {
	// Build immediately on startup
	buildFileList()

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("❌ Failed to start fsnotify: %v. Falling back to manual polling.", err)
		for {
			time.Sleep(5 * time.Minute)
			buildFileList()
		}
		return
	}
	defer watcher.Close()

	// Function to recursively add directories to the watcher
	addDirs := func(root string) {
		filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if info != nil && info.IsDir() {
				watcher.Add(path)
			}
			return nil
		})
	}

	addDirs(mediaDir)
	log.Println("👀 Watching media directory for changes...")

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok { return }
			if event.Has(fsnotify.Create) || event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) || event.Has(fsnotify.Write) {
				// If a new directory is created, add it to the watcher
				info, err := os.Stat(event.Name)
				if err == nil && info.IsDir() {
					addDirs(event.Name)
				}
				triggerRebuild()
			}
		case err, ok := <-watcher.Errors:
			if !ok { return }
			log.Printf("Watcher error: %v", err)
		}
	}
}

func startHDMICheck() {
	if getEnv("HDMI_CHECK", "no") != "yes" {
		log.Println("ℹ️ HDMI_CHECK is disabled in environment.")
		return
	}
	tvIP := getEnv("HDMI_CHECK_IP", "")
	if tvIP == "" {
		log.Println("⚠️ HDMI_CHECK enabled but HDMI_CHECK_IP is not set, skipping...")
		return
	}

	log.Printf("🚀 Starting WebOS HDMI Monitor targeting IP: %s", tvIP)
	go func() {
		for {
			runLGWebOSMonitor(tvIP)
			time.Sleep(10 * time.Second)
		}
	}()
}

func runLGWebOSMonitor(tvIP string) {
	keyFile := "/ds-data/.lgtv_key"
	u := url.URL{Scheme: "wss", Host: fmt.Sprintf("%s:3001", tvIP), Path: "/"}

	dialer := *websocket.DefaultDialer
	dialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	c, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		log.Printf("❌ WebOS Connection Failed to %s: %v (Retrying in 10s...)", tvIP, err)
		return 
	}
	
	log.Printf("✅ Connected to WebOS WebSocket at %s", tvIP)
	
	// Create context to signal the background ticker loop to exit safely upon websocket breakdown
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		c.Close()
	}()

	registerPayloadMap := map[string]interface{}{
		"forcePairing": false,
		"pairingType":  "PROMPT",
		"manifest": map[string]interface{}{
			"manifestVersion": 1,
			"permissions":     []string{"READ_SETTINGS"},
		},
	}

	if savedKey, err := os.ReadFile(keyFile); err == nil && len(savedKey) > 0 {
		registerPayloadMap["client-key"] = string(savedKey)
	}

	payloadBytes, _ := json.Marshal(registerPayloadMap)
	registerMsg := WebOSMessage{
		Type:    "register",
		ID:      "register_0",
		Payload: payloadBytes,
	}

	writeMutex := &sync.Mutex{}

	writeMutex.Lock()
	err = c.WriteJSON(registerMsg)
	writeMutex.Unlock()
	
	if err != nil {
		log.Printf("❌ WebOS Registration Write Error: %v", err)
		return
	}
	log.Println("📡 Sent pairing/registration payload to TV...")

	var lastFormat string

	for {
		var msg WebOSMessage
		if err := c.ReadJSON(&msg); err != nil {
			log.Printf("⚠️ WebOS Socket Disconnected or Read Error: %v", err)
			return 
		}

		if msg.ID == "register_0" && msg.Type == "registered" {
			log.Println("🔓 WebOS Registration SUCCESS! Listening for picture settings...")
			
			var responsePayload map[string]interface{}
			json.Unmarshal(msg.Payload, &responsePayload)
			if key, ok := responsePayload["client-key"].(string); ok {
				os.WriteFile(keyFile, []byte(key), 0644)
			}

			// Background loop tracking picture formats
			go func(ctx context.Context) {
				ticker := time.NewTicker(3 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						reqMsg := WebOSMessage{
							Type: "request",
							ID:   "req_settings",
							URI:  "ssap://settings/getSystemSettings",
							Payload: json.RawMessage(`{
								"category": "picture",
								"keys": ["pictureMode"]
							}`),
						}
						
						writeMutex.Lock()
						err := c.WriteJSON(reqMsg)
						writeMutex.Unlock()
						
						if err != nil {
							return
						}
					}
				}
			}(ctx)
			
			firstReq := WebOSMessage{
				Type: "request",
				ID:   "req_settings",
				URI:  "ssap://settings/getSystemSettings",
				Payload: json.RawMessage(`{
					"category": "picture",
					"keys": ["pictureMode"]
				}`),
			}
			writeMutex.Lock()
			c.WriteJSON(firstReq)
			writeMutex.Unlock()

		} else if msg.ID == "register_0" && msg.Type == "error" {
			log.Println("❌ LG TV Registration rejected or timed out. Please accept the prompt on the TV.")
			return
		}

		if msg.ID == "req_settings" {
			var settingsPayload SettingsPayload
			if err := json.Unmarshal(msg.Payload, &settingsPayload); err == nil {
				rawMode := settingsPayload.Settings.PictureMode
				lowerMode := strings.ToLower(rawMode)
				
				var hdmiFormat string
				switch {
				case strings.HasPrefix(lowerMode, "dolby"):
					hdmiFormat = "Dolby Vision"
				case strings.HasPrefix(lowerMode, "hdr"):
					hdmiFormat = "HDR10"
				case rawMode != "":
					hdmiFormat = "SDR"
				default:
					hdmiFormat = "Unknown / No Signal"
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
	return &ThrottledReader{
		r:           r,
		bytesPerSec: bps,
		bucketSize:  bps * 2, 
		tokens:      bps,
		lastCheck:   time.Now(),
	}
}

func (tr *ThrottledReader) Read(p []byte) (int, error) {
	tr.mu.Lock()
	now := time.Now()
	elapsed := now.Sub(tr.lastCheck).Seconds()
	tr.lastCheck = now

	tr.tokens += elapsed * tr.bytesPerSec
	if tr.tokens > tr.bucketSize {
		tr.tokens = tr.bucketSize
	}

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
	port := getEnv("PORT", "8282")

	go monitorMediaDirectory()
	go startHDMICheck()

	// Web Dashboard Handler
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(indexHTML))
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

	// TV Play API endpoint - uses ADB to send Intent directly to the TV App
	http.HandleFunc("/api/play_on_tv", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		targetUrl := r.URL.Query().Get("url")
		if targetUrl == "" {
			http.Error(w, "Missing URL", http.StatusBadRequest)
			return
		}
		
		tvIP := getEnv("TV_IP", "")
		if tvIP == "" || tvIP == "0.0.0.0" {
			http.Error(w, "TV_IP not configured", http.StatusInternalServerError)
			return
		}

		cmd := exec.Command("adb", "-s", tvIP+":5555", "shell", "am", "start", "-n", "com.example.tvstream/.PlayerActivity", "-e", "URL", targetUrl)
		if err := cmd.Run(); err != nil {
			log.Printf("ADB Error casting to TV: %v", err)
			http.Error(w, fmt.Sprintf("ADB Error: %v", err), http.StatusInternalServerError)
			return
		}
		
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
		if err != nil {
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}
		defer file.Close()

		stat, err := file.Stat()
		if err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		rangeHeader := r.Header.Get("Range")
		if rangeHeader == "" || strings.HasPrefix(rangeHeader, "bytes=0-") {
			runtimeMutex.RLock()
			hdrType, exists := runtimeMap[reqPath]
			runtimeMutex.RUnlock()
			if !exists {
				hdrType = "Scanning..."
			}
			log.Printf("▶️ NOW PLAYING: %s | (%s)", reqPath, r.RemoteAddr)
			log.Printf("   [%s]", hdrType)
		}

		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Type", "video/x-matroska")

		var start, end int64
		end = stat.Size() - 1

		if rangeHeader != "" {
			parts := strings.Split(strings.TrimPrefix(rangeHeader, "bytes="), "-")
			start, _ = strconv.ParseInt(parts[0], 10, 64)
			if len(parts) > 1 && parts[1] != "" {
				end, _ = strconv.ParseInt(parts[1], 10, 64)
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, stat.Size()))
			w.WriteHeader(http.StatusPartialContent)
		} else {
			w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
			w.WriteHeader(http.StatusOK)
		}

		_, _ = file.Seek(start, io.SeekStart)
		limit := end - start + 1

		throttledFile := NewThrottledReader(io.LimitReader(file, limit), 20000000)

		buf := make([]byte, 64*1024)
		_, _ = io.CopyBuffer(w, throttledFile, buf)
	})

	http.Handle("/download/", http.StripPrefix("/download/", http.FileServer(http.Dir("/app/public"))))

	log.Printf("Direct Play Backend server running on port %s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}
