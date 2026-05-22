package com.example.tvstream

import android.content.Intent
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
import androidx.media3.exoplayer.trackselection.DefaultTrackSelector
import androidx.media3.exoplayer.upstream.DefaultAllocator
import androidx.media3.ui.PlayerView

class AmlogicDolbyVisionCodecSelector : MediaCodecSelector {
    override fun getDecoderInfos(
        mimeType: String,
        requiresSecureDecoder: Boolean,
        requiresTunnelingProvider: Boolean
    ): List<MediaCodecInfo> {
        val defaultDecoders = MediaCodecSelector.DEFAULT.getDecoderInfos(
            mimeType,
            requiresSecureDecoder,
            requiresTunnelingProvider
        ).toMutableList()

        if (mimeType != MimeTypes.VIDEO_DOLBY_VISION && mimeType != "video/dolby-vision") {
            return defaultDecoders
        }

        defaultDecoders.sortWith(Comparator { codec1, codec2 ->
            val name1 = codec1.name.lowercase()
            val name2 = codec2.name.lowercase()

            val isHevc1 = name1.contains("dvhe")
            val isHevc2 = name2.contains("dvhe")

            if (isHevc1 && !isHevc2) return@Comparator -1
            if (!isHevc1 && isHevc2) return@Comparator 1

            val isAv11 = name1.contains("dav1")
            val isAv12 = name2.contains("dav1")

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

	// High-performance OSD Overlay
	private lateinit var osdToast: TextView
	private val osdHandler = Handler(Looper.getMainLooper())
	private val hideOsdRunnable = Runnable { osdToast.visibility = View.GONE }

	private val watchdogHandler = Handler(Looper.getMainLooper())
	private var isFirstFrameRendered = false
	private var retryCount = 0

	// Delay constants
	private val DECODER_FLUSH_STARTUP_DELAY_MS = 600L
	private val DECODER_FLUSH_RETRY_DELAY_MS = 1500L
	private val SEEK_INCREMENT_MS = 15000L

	// Smart Seek Accumulator 
	private var pendingSeekPositionMs = C.TIME_UNSET
	private val seekHandler = Handler(Looper.getMainLooper())
	
	private val executeSeekRunnable = Runnable {
		if (pendingSeekPositionMs != C.TIME_UNSET) {
			val targetPos = pendingSeekPositionMs
			pendingSeekPositionMs = C.TIME_UNSET
			
			releasePlayer()
			
			watchdogHandler.postDelayed({
				videoUrl?.let { initializePlayer(it, targetPos) }
			}, 500) 
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
	}

    override fun onNewIntent(intent: Intent?) {
        super.onNewIntent(intent)
        setIntent(intent)
        val newUrl = intent?.getStringExtra("URL")
        if (newUrl != null && newUrl != videoUrl) {
            videoUrl = newUrl
            releasePlayer()
            watchdogHandler.postDelayed({
                initializePlayer(newUrl, 0L)
            }, DECODER_FLUSH_STARTUP_DELAY_MS)
        }
    }

	override fun onStart() {
		super.onStart()
		watchdogHandler.postDelayed({
			if (player == null && videoUrl != null) {
				initializePlayer(videoUrl!!, 0L)
			}
		}, DECODER_FLUSH_STARTUP_DELAY_MS)
	}

	private fun formatTime(timeMs: Long): String {
		val totalMs = maxOf(0L, timeMs)
		val hours = totalMs / 3600000
		val minutes = (totalMs % 3600000) / 60000
		val seconds = (totalMs % 60000) / 1000
		return String.format("%d:%02d:%02d", hours, minutes, seconds)
	}

	private fun initializePlayer(url: String, startPositionMs: Long) {
		isFirstFrameRendered = false
		watchdogHandler.removeCallbacksAndMessages(null)
		
		// Force WindowManager to recreate the hardware surface
		playerView.visibility = View.VISIBLE

		val allocator = DefaultAllocator(true, C.DEFAULT_BUFFER_SEGMENT_SIZE)
		val loadControl = DefaultLoadControl.Builder()
			.setAllocator(allocator)
			.setBufferDurationsMs(
				1500,   // minBufferMs
				5000,   // maxBufferMs
				1000,   // bufferForPlaybackMs
				1500    // bufferForPlaybackAfterRebufferMs
			)
			.setTargetBufferBytes(32 * 1024 * 1024)
			.setPrioritizeTimeOverSizeThresholds(false)
			.setBackBuffer(0, false)
			.build()

		val renderersFactory = object : DefaultRenderersFactory(this) {
			override fun buildVideoRenderers(
				context: android.content.Context,
				extensionRendererMode: Int,
				mediaCodecSelector: MediaCodecSelector,
				enableDecoderFallback: Boolean,
				eventHandler: android.os.Handler,
				eventListener: androidx.media3.exoplayer.video.VideoRendererEventListener,
				allowedVideoJoiningTimeMs: Long,
				out: java.util.ArrayList<androidx.media3.exoplayer.Renderer>
			) {
				val videoRenderer = object : androidx.media3.exoplayer.video.MediaCodecVideoRenderer(
					context,
					mediaCodecSelector,
					allowedVideoJoiningTimeMs,
					enableDecoderFallback,
					eventHandler,
					eventListener,
					50 // MAX_DROPPED_VIDEO_FRAME_COUNT_TO_NOTIFY
				) {
					override fun getDecoderInfos(
						codecSelector: MediaCodecSelector,
						format: androidx.media3.common.Format,
						requiresSecureDecoder: Boolean
					): MutableList<MediaCodecInfo> {
						val codecs = format.codecs?.lowercase() ?: ""
						if (enableDecoderFallback && (codecs.contains("dvhe.07") || codecs.contains("dvh1.07"))) {
							return MediaCodecSelector.DEFAULT.getDecoderInfos(
								MimeTypes.VIDEO_H265,
								requiresSecureDecoder,
								false
							).toMutableList()
						}
						return super.getDecoderInfos(codecSelector, format, requiresSecureDecoder).toMutableList()
					}
				}
				out.add(videoRenderer)
			}
		}
		
		renderersFactory.setMediaCodecSelector(AmlogicDolbyVisionCodecSelector())
		renderersFactory.setEnableDecoderFallback(BuildConfig.FALLBACK)

		val trackSelector = DefaultTrackSelector(this)

		player = ExoPlayer.Builder(this)
			.setRenderersFactory(renderersFactory)
			.setTrackSelector(trackSelector)
			.setLoadControl(loadControl)
			.build()

		playerView.player = player
		player?.videoScalingMode = C.VIDEO_SCALING_MODE_SCALE_TO_FIT

		player?.addListener(object : Player.Listener {
			override fun onPlaybackStateChanged(playbackState: Int) {
				if (playbackState == Player.STATE_READY && player?.playWhenReady == true) {
					if (!isFirstFrameRendered) {
						watchdogHandler.postDelayed({
							if (!isFirstFrameRendered && player != null) {
								player?.pause()
								player?.play() 
								
								watchdogHandler.postDelayed({
									if (!isFirstFrameRendered) {
										showToast("Codec stalled in reclaim loop. Forcing surface reset...")
										retryPlayback(startPositionMs)
									}
								}, 2500)
							}
						}, 1500)
					}
				}
			}

			override fun onRenderedFirstFrame() {
				isFirstFrameRendered = true
				retryCount = 0 
				watchdogHandler.removeCallbacksAndMessages(null)
			}

			override fun onPlayerError(error: PlaybackException) {
				val cause = error.cause
				if (cause is MediaCodecRenderer.DecoderInitializationException) {
					showToast("Decoder failed. Retrying... ($retryCount)")
					retryPlayback(startPositionMs)
				} else {
					showToast("Error: ${error.message}")
					if (retryCount < 5) {
						retryPlayback(startPositionMs)
					}
				}
			}
		})

		val mediaItemBuilder = MediaItem.Builder().setUri(url)
		player?.setMediaItem(mediaItemBuilder.build())
		
		if (startPositionMs > 0) {
			player?.seekTo(startPositionMs)
		}
		
		player?.prepare()
		player?.play()
	}

	private fun retryPlayback(pos: Long) {
		watchdogHandler.removeCallbacksAndMessages(null)
		retryCount++
		
		releasePlayer()
		
		watchdogHandler.postDelayed({
			videoUrl?.let { initializePlayer(it, pos) }
		}, DECODER_FLUSH_RETRY_DELAY_MS)
	}

	private fun releasePlayer() {
		seekHandler.removeCallbacks(executeSeekRunnable)
		watchdogHandler.removeCallbacksAndMessages(null)
		// Removed osdHandler callback clear to allow the 2-second timeout to persist across engine recreation loops
		
		val playerToRelease = player ?: return
		
		playerToRelease.playWhenReady = false
		
		// Explicitly clear the surface attachment from the ExoPlayer side
		playerToRelease.clearVideoSurface()
		
		player = null 
		playerView.player = null
		
		// Hide the View. This signals Android's WindowManager to fully 
		// destroy the hardware surface, breaking the Amlogic driver lock.
		playerView.visibility = View.GONE
		
		try {
			playerToRelease.stop()
			playerToRelease.clearMediaItems()
			playerToRelease.release()
		} catch (e: Exception) {
			e.printStackTrace()
		}
	}

	private fun cycleTracks(trackType: Int, allowDisable: Boolean): String {
		val activePlayer = player ?: return "Player not ready"
		val currentTracks = activePlayer.currentTracks
		
		class TrackDescriptor(val group: Tracks.Group, val trackIndex: Int)
		val availableOptions = mutableListOf<TrackDescriptor?>()
		
		if (allowDisable) {
			availableOptions.add(null) 
		}
		
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
		
		if (availableOptions.isEmpty()) {
			return "None available"
		}
		
		val nextSelectionIndex = (activeSelectionIndex + 1) % availableOptions.size
		val chosenTrack = availableOptions[nextSelectionIndex]
		val selectionBuilder = activePlayer.trackSelectionParameters.buildUpon()
		
		val trackName: String
		if (chosenTrack == null) {
			selectionBuilder.setTrackTypeDisabled(trackType, true)
			trackName = "Off"
		} else {
			selectionBuilder.setTrackTypeDisabled(trackType, false)
			selectionBuilder.setOverrideForType(
				TrackSelectionOverride(
					chosenTrack.group.mediaTrackGroup,
					chosenTrack.trackIndex
				)
			)
			
			val format = chosenTrack.group.getTrackFormat(chosenTrack.trackIndex)
			val label = format.label
			val lang = format.language
			
			trackName = label ?: lang ?: "Track ${chosenTrack.trackIndex + 1}"
		}
		
		activePlayer.trackSelectionParameters = selectionBuilder.build()
		return trackName
	}

	private fun showToast(message: String) {
		if (!BuildConfig.SHOW_TOASTS) return
		
		osdToast.text = message
		osdToast.visibility = View.VISIBLE
		
		osdHandler.removeCallbacks(hideOsdRunnable)
		osdHandler.postDelayed(hideOsdRunnable, 2000) 
	}

	override fun onKeyDown(keyCode: Int, event: KeyEvent?): Boolean {
		if (event == null) return super.onKeyDown(keyCode, event)
		
		when (keyCode) {
			KeyEvent.KEYCODE_BACK -> {
				if (event.repeatCount == 0) {
					// 1. Instantly stop decoding and destroy the video surface hardware link
					releasePlayer()
					
					// 2. Give the Amlogic SoC 150 milliseconds to successfully flush the
					// BT.2020 color space lock and return the display pipeline to BT.709 SDR.
					// If we kill the Activity immediately, the SDR file list menu will render inside
					// the stuck HDR bounds, causing washed-out/pink color bugs.
					Handler(Looper.getMainLooper()).postDelayed({
						finish()
					}, 150)
				}
				return true
			}
			KeyEvent.KEYCODE_DPAD_LEFT, KeyEvent.KEYCODE_MEDIA_REWIND -> {
				player?.let {
					val duration = it.duration
					if (duration == C.TIME_UNSET) return true

					if (pendingSeekPositionMs == C.TIME_UNSET) {
						pendingSeekPositionMs = it.currentPosition
					}
					
					pendingSeekPositionMs = maxOf(0L, pendingSeekPositionMs - SEEK_INCREMENT_MS)
					showToast("${formatTime(pendingSeekPositionMs)} / ${formatTime(duration)}")
					
					seekHandler.removeCallbacks(executeSeekRunnable)
					seekHandler.postDelayed(executeSeekRunnable, 800)
				}
				return true
			}
			KeyEvent.KEYCODE_DPAD_RIGHT, KeyEvent.KEYCODE_MEDIA_FAST_FORWARD -> {
				player?.let {
					val duration = it.duration
					if (duration == C.TIME_UNSET) return true

					if (pendingSeekPositionMs == C.TIME_UNSET) {
						pendingSeekPositionMs = it.currentPosition
					}
					
					pendingSeekPositionMs = minOf(duration, pendingSeekPositionMs + SEEK_INCREMENT_MS)
					showToast("${formatTime(pendingSeekPositionMs)} / ${formatTime(duration)}")
					
					seekHandler.removeCallbacks(executeSeekRunnable)
					seekHandler.postDelayed(executeSeekRunnable, 800)
				}
				return true
			}
			KeyEvent.KEYCODE_DPAD_UP -> {
				if (event.repeatCount == 0) {
					val trackInfo = cycleTracks(C.TRACK_TYPE_TEXT, allowDisable = true)
					showToast("Subtitle: $trackInfo")
				}
				return true
			}
			KeyEvent.KEYCODE_DPAD_DOWN -> {
				if (event.repeatCount == 0) {
					val trackInfo = cycleTracks(C.TRACK_TYPE_AUDIO, allowDisable = false)
					showToast("Audio: $trackInfo")
				}
				return true
			}
			KeyEvent.KEYCODE_DPAD_CENTER,
			KeyEvent.KEYCODE_MEDIA_PLAY_PAUSE,
			KeyEvent.KEYCODE_ENTER,
			KeyEvent.KEYCODE_SPACE,
			KeyEvent.KEYCODE_MEDIA_PLAY,
			KeyEvent.KEYCODE_MEDIA_PAUSE -> {
				if (event.repeatCount == 0) {
					player?.let {
						if (it.isPlaying) {
							it.pause()
						} else {
							it.play()
						}
					}
				}
				return true 
			}
		}
		
		return super.onKeyDown(keyCode, event)
	}

	override fun onPause() {
		super.onPause()
		releasePlayer()
		osdHandler.removeCallbacksAndMessages(null) 
	}

	override fun onStop() {
		super.onStop()
		releasePlayer()
		osdHandler.removeCallbacksAndMessages(null)
	}

	override fun onDestroy() {
		super.onDestroy()
		releasePlayer()
		osdHandler.removeCallbacksAndMessages(null)
	}
}
