package main

import (
	"bytes"
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

// WebOSMessage represents the standard LG WebOS websocket envelope
type WebOSMessage struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	URI     string          `json:"uri,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// SettingsPayload represents the parsed settings from the TV
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
		return
	}
	tvIP := getEnv("HDMI_CHECK_IP", "")
	if tvIP == "" {
		log.Println("HDMI_CHECK enabled but HDMI_CHECK_IP is not set, skipping...")
		return
	}

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
		return 
	}
	defer c.Close()

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

	if err := c.WriteJSON(registerMsg); err != nil {
		return
	}

	var lastFormat string
	writeMutex := &sync.Mutex{}

	for {
		var msg WebOSMessage
		if err := c.ReadJSON(&msg); err != nil {
			return 
		}

		if msg.ID == "register_0" && msg.Type == "registered" {
			var responsePayload map[string]interface{}
			json.Unmarshal(msg.Payload, &responsePayload)
			if key, ok := responsePayload["client-key"].(string); ok {
				os.WriteFile(keyFile, []byte(key), 0644)
			}

			go func() {
				ticker := time.NewTicker(3 * time.Second)
				defer ticker.Stop()
				for range ticker.C {
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
			}()
			
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
			log.Println("LG TV Registration error. Please accept the prompt on the TV.")
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
