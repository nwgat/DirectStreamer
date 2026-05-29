package ninja.nwgat.directstreamer

import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.KeyEvent
import android.view.View
import android.view.WindowManager
import android.widget.TextView
import androidx.appcompat.app.AppCompatActivity
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.MimeTypes
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.Tracks
import androidx.media3.common.TrackSelectionOverride
import androidx.media3.exoplayer.DefaultLoadControl
import androidx.media3.exoplayer.DefaultRenderersFactory
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.mediacodec.MediaCodecInfo
import androidx.media3.exoplayer.mediacodec.MediaCodecRenderer
import androidx.media3.exoplayer.mediacodec.MediaCodecSelector
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.exoplayer.source.MergingMediaSource
import androidx.media3.exoplayer.trackselection.DefaultTrackSelector
import androidx.media3.exoplayer.upstream.DefaultAllocator
import androidx.media3.ui.PlayerView

private data class TrackDescriptor(val group: Tracks.Group, val trackIndex: Int)

class AmlogicDolbyVisionCodecSelector : MediaCodecSelector {
    override fun getDecoderInfos(mimeType: String, requiresSecureDecoder: Boolean, requiresTunnelingProvider: Boolean): List<MediaCodecInfo> {
        val defaultDecoders = MediaCodecSelector.DEFAULT.getDecoderInfos(mimeType, requiresSecureDecoder, requiresTunnelingProvider).toMutableList()
        if (mimeType != MimeTypes.VIDEO_DOLBY_VISION && mimeType != "video/dolby-vision") return defaultDecoders
        defaultDecoders.sortWith(Comparator { codec1, codec2 ->
            val name1 = codec1.name.lowercase(); val name2 = codec2.name.lowercase()
            val isHevc1 = name1.contains("dvhe"); val isHevc2 = name2.contains("dvhe")
            if (isHevc1 && !isHevc2) return@Comparator -1
            if (!isHevc1 && isHevc2) return@Comparator 1
            val isAv11 = name1.contains("dav1"); val isAv12 = name2.contains("dav1")
            if (isAv11 && !isAv12) return@Comparator 1
            if (!isAv11 && isAv12) return@Comparator -1
            0
        })
        return defaultDecoders
    }
}

class PlayerActivity : AppCompatActivity() {
	private var player: ExoPlayer? = null
	private lateinit var playerView: PlayerView
	private var videoUrl: String? = null
	private var subtitleUrl: String? = null
	private var audioUrl: String? = null
    private var hdrType: String = ""

	private lateinit var osdToast: TextView
	private val osdHandler = Handler(Looper.getMainLooper())
	private val hideOsdRunnable = Runnable { osdToast.visibility = View.GONE }
	private val watchdogHandler = Handler(Looper.getMainLooper())
	private var isFirstFrameRendered = false
	private var retryCount = 0

	private val DECODER_FLUSH_STARTUP_DELAY_MS = 600L
	private val DECODER_FLUSH_RETRY_DELAY_MS = 1500L
	private val SEEK_INCREMENT_MS = 15000L

	private var pendingSeekPositionMs = C.TIME_UNSET
	private val seekHandler = Handler(Looper.getMainLooper())
	
	private val executeSeekRunnable = Runnable {
		if (pendingSeekPositionMs != C.TIME_UNSET) {
			val targetPos = pendingSeekPositionMs
			pendingSeekPositionMs = C.TIME_UNSET
			releasePlayer()
			watchdogHandler.postDelayed({ videoUrl?.let { initializePlayer(it, subtitleUrl, audioUrl, targetPos) } }, 500) 
		}
	}

	override fun onCreate(savedInstanceState: Bundle?) {
		super.onCreate(savedInstanceState)
		window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
		setContentView(R.layout.activity_player)
		playerView = findViewById(R.id.player_view)
		osdToast = findViewById(R.id.osd_toast)
		playerView.useController = false
		
		videoUrl = intent.getStringExtra("URL") ?: return
		subtitleUrl = intent.getStringExtra("SUBTITLE_URL")
		audioUrl = intent.getStringExtra("AUDIO_URL")
        hdrType = intent.getStringExtra("HDR_TYPE") ?: ""
	}

    override fun onNewIntent(intent: Intent?) {
        super.onNewIntent(intent)
        setIntent(intent)
        val newUrl = intent?.getStringExtra("URL")
        val newSub = intent?.getStringExtra("SUBTITLE_URL")
        val newAudio = intent?.getStringExtra("AUDIO_URL")
        val newHdr = intent?.getStringExtra("HDR_TYPE") ?: ""
        
        if (newUrl != null && (newUrl != videoUrl || newSub != subtitleUrl || newHdr != hdrType || newAudio != audioUrl)) {
            videoUrl = newUrl
            subtitleUrl = newSub
            audioUrl = newAudio
            hdrType = newHdr
            releasePlayer()
            watchdogHandler.postDelayed({ initializePlayer(newUrl, newSub, newAudio, 0L) }, DECODER_FLUSH_STARTUP_DELAY_MS)
        }
    }

	override fun onStart() {
		super.onStart()
		watchdogHandler.postDelayed({
			if (player == null && videoUrl != null) initializePlayer(videoUrl!!, subtitleUrl, audioUrl, 0L)
		}, DECODER_FLUSH_STARTUP_DELAY_MS)
	}

	private fun formatTime(timeMs: Long): String {
		val totalMs = maxOf(0L, timeMs)
		val hours = totalMs / 3600000
		val minutes = (totalMs % 3600000) / 60000
		val seconds = (totalMs % 60000) / 1000
		return String.format("%d:%02d:%02d", hours, minutes, seconds)
	}

	private fun initializePlayer(url: String, subUrl: String?, extAudioUrl: String?, startPositionMs: Long) {
		isFirstFrameRendered = false
		watchdogHandler.removeCallbacksAndMessages(null)
		playerView.visibility = View.VISIBLE
		var hasForcedExternalSub = false
		var hasForcedExternalAudio = false
        var hasSeekedForExternalAudio = false

		val allocator = DefaultAllocator(true, C.DEFAULT_BUFFER_SEGMENT_SIZE)
		val loadControl = DefaultLoadControl.Builder().setAllocator(allocator)
			.setBufferDurationsMs(32000, 64000, 2500, 5000)
			.setTargetBufferBytes(128 * 1024 * 1024)
			.setPrioritizeTimeOverSizeThresholds(true)
			.setBackBuffer(0, false).build()

		val renderersFactory = object : DefaultRenderersFactory(this) {
			override fun buildVideoRenderers(
				context: android.content.Context, extensionRendererMode: Int,
				mediaCodecSelector: MediaCodecSelector, enableDecoderFallback: Boolean,
				eventHandler: android.os.Handler, eventListener: androidx.media3.exoplayer.video.VideoRendererEventListener,
				allowedVideoJoiningTimeMs: Long, out: java.util.ArrayList<androidx.media3.exoplayer.Renderer>
			) {
				val videoRenderer = object : androidx.media3.exoplayer.video.MediaCodecVideoRenderer(
					context, mediaCodecSelector, allowedVideoJoiningTimeMs, enableDecoderFallback, eventHandler, eventListener, 50
				) {
					override fun getDecoderInfos(codecSelector: MediaCodecSelector, format: androidx.media3.common.Format, requiresSecureDecoder: Boolean): MutableList<MediaCodecInfo> {
						val codecs = format.codecs?.lowercase() ?: ""
                        val isProfile7 = codecs.contains("dvhe.07") || codecs.contains("dvh1.07") || hdrType.contains("Profile 7")
						if (enableDecoderFallback && isProfile7) {
						    android.util.Log.w("DirectStreamer", "⚠️ [FALLBACK TRIGGERED] Dolby Vision Profile 7 detected -> Forcing HDR10/HEVC substitution")
							return MediaCodecSelector.DEFAULT.getDecoderInfos(MimeTypes.VIDEO_H265, requiresSecureDecoder, false).toMutableList()
						}
						
						val infos = super.getDecoderInfos(codecSelector, format, requiresSecureDecoder).toMutableList()
						
						val isProfile5or8 = codecs.contains("dvhe.05") || codecs.contains("dvh1.05") || codecs.contains("dvhe.08") || codecs.contains("dvh1.08") || hdrType.contains("Profile 5") || hdrType.contains("Profile 8")
						if (AppConfig.dvforce && isProfile5or8 && format.sampleMimeType == MimeTypes.VIDEO_DOLBY_VISION) {
                            val targetName = "c2.amlogic.dolby-vision.dvhe.decoder"
                            val targetInfo = infos.find { it.name.equals(targetName, ignoreCase = true) }
                            if (targetInfo != null) {
                                infos.remove(targetInfo)
                                infos.add(0, targetInfo)
                                android.util.Log.i("DirectStreamer", "🎯 [DV-FORCE] Prioritized $targetName for DV Profile 5/8")
                            } else {
                                android.util.Log.w("DirectStreamer", "⚠️ [DV-FORCE] Target decoder $targetName not found in available codecs!")
                            }
						}

						return infos
					}
				}
				out.add(videoRenderer)
			}
		}
		
		renderersFactory.setMediaCodecSelector(AmlogicDolbyVisionCodecSelector())
		renderersFactory.setEnableDecoderFallback(AppConfig.fallback)

		val trackSelector = DefaultTrackSelector(this)
        val trackParams = trackSelector.buildUponParameters()
            .setPreferredTextLanguage("en")
            .setSelectUndeterminedTextLanguage(true)
            
        if (AppConfig.subtitles == "off") {
            trackParams.setTrackTypeDisabled(C.TRACK_TYPE_TEXT, true)
        }

        if (extAudioUrl != null) {
            trackParams.setTrackTypeDisabled(C.TRACK_TYPE_AUDIO, true)
        }

		trackSelector.setParameters(trackParams)

		player = ExoPlayer.Builder(this).setRenderersFactory(renderersFactory).setTrackSelector(trackSelector).setLoadControl(loadControl).build()
		
		val audioAttributes = androidx.media3.common.AudioAttributes.Builder()
			.setUsage(C.USAGE_MEDIA)
			.setContentType(C.AUDIO_CONTENT_TYPE_MOVIE)
			.build()
		player?.setAudioAttributes(audioAttributes, true)
		
		playerView.player = player
		player?.videoScalingMode = C.VIDEO_SCALING_MODE_SCALE_TO_FIT

		player?.addListener(object : Player.Listener {
			override fun onPlaybackStateChanged(playbackState: Int) {
				if (playbackState == Player.STATE_READY) {
                    
                    var trackChanged = false

                    if (AppConfig.subtitles != "off" && subUrl != null && !hasForcedExternalSub) {
                        player?.let { activePlayer ->
                            val tracks = activePlayer.currentTracks
                            for (group in tracks.groups) {
                                if (group.type == C.TRACK_TYPE_TEXT) {
                                    for (i in 0 until group.length) {
                                        val format = group.getTrackFormat(i)
                                        if (format.id == "ext-srt" || format.label == "External (SRT)") {
                                            hasForcedExternalSub = true
                                            trackChanged = true
                                            activePlayer.trackSelectionParameters = activePlayer.trackSelectionParameters.buildUpon()
                                                .setOverrideForType(TrackSelectionOverride(group.mediaTrackGroup, i))
                                                .setTrackTypeDisabled(C.TRACK_TYPE_TEXT, false)
                                                .build()
                                            android.util.Log.i("DirectStreamer", "Loaded: External (SRT)")
                                            break
                                        }
                                    }
                                }
                                if (hasForcedExternalSub) break
                            }
                        }
                    }

                    if (extAudioUrl != null && !hasForcedExternalAudio) {
                        player?.let { activePlayer ->
                            var lastAudioGroup: Tracks.Group? = null
                            val tracks = activePlayer.currentTracks
                            for (i in tracks.groups.indices) {
                                val group = tracks.groups[i]
                                if (group.type == C.TRACK_TYPE_AUDIO) {
                                    lastAudioGroup = group
                                }
                            }
                            if (lastAudioGroup != null) {
                                hasForcedExternalAudio = true
                                trackChanged = true
                                
                                activePlayer.trackSelectionParameters = activePlayer.trackSelectionParameters.buildUpon()
                                    .setTrackTypeDisabled(C.TRACK_TYPE_AUDIO, false)
                                    .clearOverridesOfType(C.TRACK_TYPE_AUDIO)
                                    .setOverrideForType(TrackSelectionOverride(lastAudioGroup.mediaTrackGroup, 0))
                                    .build()
                                
                                // Initial flush for MergingMediaSource
                                activePlayer.seekTo(activePlayer.currentPosition)
                                
                                android.util.Log.i("DirectStreamer", "Loaded: External Audio Track")
                            }
                        }
                    }

                    if (trackChanged) return

                    if (startPositionMs > 0 && extAudioUrl != null && !hasSeekedForExternalAudio) {
                        hasSeekedForExternalAudio = true
                        player?.seekTo(startPositionMs)
                        player?.play()
                        return
                    }

                    if (player?.playWhenReady == true && !isFirstFrameRendered) {
					    watchdogHandler.postDelayed({
						    if (!isFirstFrameRendered && player != null) {
							    player?.pause(); player?.play() 
							    watchdogHandler.postDelayed({ if (!isFirstFrameRendered) { showToast("Codec stalled in reclaim loop. Forcing surface reset..."); retryPlayback(player?.currentPosition ?: startPositionMs) } }, 2500)
						    }
					    }, 1500)
				    }
                }
			}
			override fun onRenderedFirstFrame() { isFirstFrameRendered = true; retryCount = 0; watchdogHandler.removeCallbacksAndMessages(null) }
			
			override fun onPlayerError(error: PlaybackException) {
			    // Capture the exact moment of failure to prevent resetting playback to 0:00!
			    val currentPos = player?.currentPosition?.takeIf { it > 0 } ?: startPositionMs
				if (error.cause is MediaCodecRenderer.DecoderInitializationException) { 
				    showToast("Decoder failed. Retrying... ($retryCount)")
				    retryPlayback(currentPos)
				} else { 
				    showToast("Error: ${error.message}")
				    if (retryCount < 5) retryPlayback(currentPos) 
				}
			}
		})

		val mediaItemBuilder = MediaItem.Builder().setUri(url)
		
		if (!subUrl.isNullOrEmpty()) {
		    val subtitleConfig = MediaItem.SubtitleConfiguration.Builder(Uri.parse(subUrl))
		        .setMimeType(MimeTypes.APPLICATION_SUBRIP)
		        .setLanguage("en")
                .setLabel("External (SRT)")
                .setId("ext-srt")
		        .setSelectionFlags(C.SELECTION_FLAG_DEFAULT or C.SELECTION_FLAG_FORCED)
		        .build()
		    mediaItemBuilder.setSubtitleConfigurations(listOf(subtitleConfig))
		}

		val videoMediaItem = mediaItemBuilder.build()

		if (!extAudioUrl.isNullOrEmpty()) {
		    val videoSource = DefaultMediaSourceFactory(this).createMediaSource(videoMediaItem)
		    
		    val audioItemBuilder = MediaItem.Builder().setUri(Uri.parse(extAudioUrl))
		    if (extAudioUrl.endsWith(".eac3", ignoreCase = true)) {
		        audioItemBuilder.setMimeType(MimeTypes.AUDIO_E_AC3)
		    } else if (extAudioUrl.endsWith(".ac3", ignoreCase = true)) {
		        audioItemBuilder.setMimeType(MimeTypes.AUDIO_AC3)
		    } else if (extAudioUrl.endsWith(".flac", ignoreCase = true)) {
		        audioItemBuilder.setMimeType(MimeTypes.AUDIO_FLAC)
		    }
		    
		    val audioItem = audioItemBuilder.build()
		    val audioSource = DefaultMediaSourceFactory(this).createMediaSource(audioItem)
		    
		    val mergingSource = MergingMediaSource(true, true, videoSource, audioSource)
		    player?.setMediaSource(mergingSource)
		} else {
		    player?.setMediaItem(videoMediaItem)
		}

		if (startPositionMs > 0 && extAudioUrl == null) {
            player?.seekTo(startPositionMs)
        }
		
		player?.prepare()
        
		if (extAudioUrl == null || startPositionMs == 0L) {
            player?.play()
        }
	}

	private fun retryPlayback(fallbackPos: Long) {
	    // Ensure we always resume cleanly from where we left off, even if a codec panics
	    val resumePos = player?.currentPosition?.takeIf { it > 0 } ?: fallbackPos
		watchdogHandler.removeCallbacksAndMessages(null); retryCount++
		releasePlayer()
		watchdogHandler.postDelayed({ videoUrl?.let { initializePlayer(it, subtitleUrl, audioUrl, resumePos) } }, DECODER_FLUSH_RETRY_DELAY_MS)
	}

	private fun releasePlayer() {
		seekHandler.removeCallbacks(executeSeekRunnable); watchdogHandler.removeCallbacksAndMessages(null)
		val playerToRelease = player ?: return
		playerToRelease.playWhenReady = false
		playerToRelease.clearVideoSurface()
		player = null; playerView.player = null; playerView.visibility = View.GONE
		try { playerToRelease.stop(); playerToRelease.clearMediaItems(); playerToRelease.release() } catch (e: Exception) { e.printStackTrace() }
	}

	private fun cycleTracks(trackType: Int, allowDisable: Boolean): String {
        try {
            val activePlayer = player ?: return "Player not ready"
            val currentTracks = activePlayer.currentTracks
            val availableOptions = mutableListOf<TrackDescriptor?>()
            
            if (allowDisable) availableOptions.add(null)
            
            var activeSelectionIndex = if (allowDisable && activePlayer.trackSelectionParameters.disabledTrackTypes.contains(trackType)) 0 else -1
            
            for (group in currentTracks.groups) {
                if (group.type == trackType) {
                    for (i in 0 until group.length) {
                        if (group.isTrackSupported(i)) {
                            availableOptions.add(TrackDescriptor(group, i))
                            if (group.isTrackSelected(i) && activeSelectionIndex == -1) {
                                activeSelectionIndex = availableOptions.lastIndex
                            }
                        }
                    }
                }
            }
            
            if (availableOptions.isEmpty()) return "None available"
            
            val nextSelectionIndex = if (activeSelectionIndex == -1) 0 else (activeSelectionIndex + 1) % availableOptions.size
            val chosenTrack = availableOptions[nextSelectionIndex]
            
            val selectionBuilder = activePlayer.trackSelectionParameters.buildUpon()
            val trackName: String
            
            if (chosenTrack == null) {
                selectionBuilder.setTrackTypeDisabled(trackType, true)
                trackName = "Off"
            } else {
                selectionBuilder.setTrackTypeDisabled(trackType, false)
                selectionBuilder.clearOverridesOfType(trackType)
                selectionBuilder.setOverrideForType(TrackSelectionOverride(chosenTrack.group.mediaTrackGroup, chosenTrack.trackIndex))
                
                val format = chosenTrack.group.getTrackFormat(chosenTrack.trackIndex)
                val readableLang = format.language?.let { lang -> 
                    val dl = java.util.Locale(lang).displayLanguage
                    if (dl.isNotEmpty()) dl.replaceFirstChar { it.uppercase() } else lang
                }
                trackName = format.label ?: readableLang ?: "Track ${chosenTrack.trackIndex + 1}"
            }
            
            // Apply parameters 
            activePlayer.trackSelectionParameters = selectionBuilder.build()
            
            // ✨ FIX: Amlogic hardware decoders often panic when the audio format is swapped mid-stream. 
            // A synchronous seekTo flushes the pipeline and allows a safe transition.
            if (trackType == C.TRACK_TYPE_AUDIO) {
                activePlayer.seekTo(activePlayer.currentPosition)
            }
            
            return trackName
            
        } catch (e: Exception) {
            e.printStackTrace()
            return "Cycle Error: ${e.message}"
        }
	}

	private fun showToast(message: String) {
		if (!AppConfig.showToasts) return
		osdToast.text = message; osdToast.visibility = View.VISIBLE
		osdHandler.removeCallbacks(hideOsdRunnable); osdHandler.postDelayed(hideOsdRunnable, 2000) 
	}

	override fun onKeyDown(keyCode: Int, event: KeyEvent?): Boolean {
		if (event == null) return super.onKeyDown(keyCode, event)
		when (keyCode) {
			KeyEvent.KEYCODE_BACK -> {
				if (event.repeatCount == 0) {
					releasePlayer()
					Handler(Looper.getMainLooper()).postDelayed({ finish() }, 150)
				}
				return true
			}
			KeyEvent.KEYCODE_DPAD_LEFT, KeyEvent.KEYCODE_MEDIA_REWIND -> {
				player?.let {
					val duration = it.duration; if (duration == C.TIME_UNSET) return true
					if (pendingSeekPositionMs == C.TIME_UNSET) pendingSeekPositionMs = it.currentPosition
					pendingSeekPositionMs = maxOf(0L, pendingSeekPositionMs - SEEK_INCREMENT_MS)
					showToast("${formatTime(pendingSeekPositionMs)} / ${formatTime(duration)}")
					seekHandler.removeCallbacks(executeSeekRunnable); seekHandler.postDelayed(executeSeekRunnable, 800)
				}
				return true
			}
			KeyEvent.KEYCODE_DPAD_RIGHT, KeyEvent.KEYCODE_MEDIA_FAST_FORWARD -> {
				player?.let {
					val duration = it.duration; if (duration == C.TIME_UNSET) return true
					if (pendingSeekPositionMs == C.TIME_UNSET) pendingSeekPositionMs = it.currentPosition
					pendingSeekPositionMs = minOf(duration, pendingSeekPositionMs + SEEK_INCREMENT_MS)
					showToast("${formatTime(pendingSeekPositionMs)} / ${formatTime(duration)}")
					seekHandler.removeCallbacks(executeSeekRunnable); seekHandler.postDelayed(executeSeekRunnable, 800)
				}
				return true
			}
			KeyEvent.KEYCODE_DPAD_UP -> { if (event.repeatCount == 0) { val trackInfo = cycleTracks(C.TRACK_TYPE_TEXT, true); showToast("Subtitle: $trackInfo") }; return true }
			KeyEvent.KEYCODE_DPAD_DOWN -> { if (event.repeatCount == 0) { val trackInfo = cycleTracks(C.TRACK_TYPE_AUDIO, false); showToast("Audio: $trackInfo") }; return true }
			KeyEvent.KEYCODE_MEDIA_PLAY -> {
				if (event.repeatCount == 0) player?.play()
				return true
			}
			KeyEvent.KEYCODE_MEDIA_PAUSE -> {
				if (event.repeatCount == 0) player?.pause()
				return true
			}
			KeyEvent.KEYCODE_DPAD_CENTER, KeyEvent.KEYCODE_MEDIA_PLAY_PAUSE, KeyEvent.KEYCODE_ENTER, KeyEvent.KEYCODE_SPACE -> {
				if (event.repeatCount == 0) player?.let { if (it.playWhenReady) it.pause() else it.play() }
				return true 
			}
		}
		return super.onKeyDown(keyCode, event)
	}

	override fun onPause() { super.onPause(); releasePlayer(); osdHandler.removeCallbacksAndMessages(null) }
	override fun onStop() { super.onStop(); releasePlayer(); osdHandler.removeCallbacksAndMessages(null) }
	override fun onDestroy() { super.onDestroy(); releasePlayer(); osdHandler.removeCallbacksAndMessages(null) }
}
