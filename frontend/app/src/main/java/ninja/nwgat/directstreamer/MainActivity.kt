package ninja.nwgat.directstreamer

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.view.KeyEvent
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.TextView
import androidx.appcompat.app.AppCompatActivity
import androidx.lifecycle.lifecycleScope
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import okhttp3.OkHttpClient
import okhttp3.Request
import org.json.JSONArray
import org.json.JSONObject
import java.io.BufferedReader
import java.io.InputStreamReader
import java.net.ServerSocket
import kotlin.concurrent.thread

data class FileItem(val name: String, val url: String, val type: String, val hdrType: String, val subtitleUrl: String? = null, val audioUrl: String? = null)

object AppConfig {
    var showToasts = true
    var fallback = true
    var dvforce = true
    var subtitles = "always"
}

object TvWebServer {
    private var isRunning = false
    fun start(context: android.content.Context, port: Int = 8080) {
        if (isRunning) return
        isRunning = true
        thread(start = true) {
            try {
                val serverSocket = ServerSocket(port)
                while (isRunning) {
                    try {
                        val client = serverSocket.accept()
                        thread {
                            try {
                                val reader = BufferedReader(InputStreamReader(client.inputStream))
                                val requestLine = reader.readLine()
                                if (requestLine != null && requestLine.startsWith("GET /play?")) {
                                    val fullPath = requestLine.substringAfter("GET ").substringBefore(" HTTP")
                                    val uri = Uri.parse("http://localhost$fullPath")
                                    val url = uri.getQueryParameter("url")
                                    val subUrl = uri.getQueryParameter("sub")
                                    val hdrType = uri.getQueryParameter("hdr")
                                    val audioUrl = uri.getQueryParameter("audio")
                                    
                                    if (url != null) {
                                        val intent = Intent(context, PlayerActivity::class.java).apply {
                                            putExtra("URL", url)
                                            subUrl?.let { putExtra("SUBTITLE_URL", it) }
                                            hdrType?.let { putExtra("HDR_TYPE", it) }
                                            audioUrl?.let { putExtra("AUDIO_URL", it) }
                                            addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP)
                                        }
                                        context.startActivity(intent)
                                    }
                                    val response = "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nAccess-Control-Allow-Origin: *\r\n\r\n{\"status\":\"success\"}"
                                    client.outputStream.write(response.toByteArray())
                                } else if (requestLine != null && requestLine.startsWith("GET /config?")) {
                                    if (requestLine.contains("show_toasts=yes")) AppConfig.showToasts = true
                                    if (requestLine.contains("show_toasts=no")) AppConfig.showToasts = false
                                    if (requestLine.contains("fallback=yes")) AppConfig.fallback = true
                                    if (requestLine.contains("fallback=no")) AppConfig.fallback = false
                                    if (requestLine.contains("dvforce=yes")) AppConfig.dvforce = true
                                    if (requestLine.contains("dvforce=no")) AppConfig.dvforce = false
                                    if (requestLine.contains("subtitles=always")) AppConfig.subtitles = "always"
                                    if (requestLine.contains("subtitles=off")) AppConfig.subtitles = "off"
                                    val response = "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nAccess-Control-Allow-Origin: *\r\n\r\n{\"status\":\"success\"}"
                                    client.outputStream.write(response.toByteArray())
                                } else {
                                    val response = "HTTP/1.1 404 Not Found\r\n\r\n"
                                    client.outputStream.write(response.toByteArray())
                                }
                                client.close()
                            } catch (e: Exception) { e.printStackTrace() }
                        }
                    } catch (e: Exception) { e.printStackTrace() }
                }
            } catch (e: Exception) { e.printStackTrace() }
        }
    }
}

class MainActivity : AppCompatActivity() {
    private lateinit var recyclerView: RecyclerView
    private lateinit var headerLayout: LinearLayout
    private val client = OkHttpClient()
    private val adapter = FileAdapter { fileItem ->
        val intent = Intent(this, PlayerActivity::class.java).apply { 
            putExtra("URL", fileItem.url) 
            putExtra("HDR_TYPE", fileItem.hdrType)
            fileItem.subtitleUrl?.let { putExtra("SUBTITLE_URL", it) }
            fileItem.audioUrl?.let { putExtra("AUDIO_URL", it) }
        }
        startActivity(intent)
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)

        TvWebServer.start(applicationContext)

        recyclerView = findViewById(R.id.recyclerView)
        headerLayout = findViewById(R.id.header_layout)
        recyclerView.layoutManager = LinearLayoutManager(this)
        recyclerView.adapter = adapter

        val prefs = getSharedPreferences("app_prefs", Context.MODE_PRIVATE)
        val currentUrl = prefs.getString("backend_url", BuildConfig.BACKEND_URL) ?: BuildConfig.BACKEND_URL

        val urlInput = findViewById<EditText>(R.id.input_backend_url)
        urlInput.setText(currentUrl)

        findViewById<Button>(R.id.btn_save_url).setOnClickListener {
            val newUrl = urlInput.text.toString().trimEnd('/')
            prefs.edit().putString("backend_url", newUrl).apply()
            fetchConfigAndFiles(newUrl)
            headerLayout.visibility = View.GONE
            recyclerView.requestFocus()
        }

        fetchConfigAndFiles(currentUrl)
    }

    override fun dispatchKeyEvent(event: KeyEvent): Boolean {
        if (event.action == KeyEvent.ACTION_DOWN) {
            if (event.keyCode == KeyEvent.KEYCODE_DPAD_UP) {
                if (recyclerView.hasFocus()) {
                    val focusedChild = recyclerView.focusedChild
                    val position = if (focusedChild != null) recyclerView.getChildAdapterPosition(focusedChild) else -1
                    
                    if (position == 0 || adapter.itemCount == 0) {
                        if (headerLayout.visibility == View.GONE) {
                            headerLayout.visibility = View.VISIBLE
                            findViewById<EditText>(R.id.input_backend_url).requestFocus()
                            return true
                        }
                    }
                }
            } else if (event.keyCode == KeyEvent.KEYCODE_DPAD_DOWN) {
                if (headerLayout.hasFocus() || headerLayout.visibility == View.VISIBLE) {
                    val focusView = currentFocus
                    if (focusView?.parent?.parent == headerLayout || focusView?.parent == headerLayout || focusView == headerLayout) {
                        headerLayout.visibility = View.GONE
                        recyclerView.requestFocus()
                        return true
                    }
                }
            }
        }
        return super.dispatchKeyEvent(event)
    }

    private fun fetchConfigAndFiles(baseUrl: String) {
        lifecycleScope.launch(Dispatchers.IO) {
            try {
                val req = Request.Builder().url("$baseUrl/api/config").build()
                client.newCall(req).execute().use { response ->
                    if (response.isSuccessful) {
                        val jsonObj = JSONObject(response.body?.string() ?: "{}")
                        AppConfig.showToasts = jsonObj.optString("show_toasts", "yes") == "yes"
                        AppConfig.fallback = jsonObj.optString("fallback", "yes") == "yes"
                        AppConfig.dvforce = jsonObj.optString("dvforce", "yes") == "yes"
                        AppConfig.subtitles = jsonObj.optString("subtitles", "always")
                    }
                }
            } catch (e: Exception) { e.printStackTrace() }

            try {
                val request = Request.Builder().url("$baseUrl/api/files").build()
                client.newCall(request).execute().use { response ->
                    if (!response.isSuccessful) return@use
                    val responseData = response.body?.string() ?: return@use
                    val jsonArray = JSONArray(responseData)
                    val files = mutableListOf<FileItem>()
                    for (i in 0 until jsonArray.length()) {
                        val obj = jsonArray.getJSONObject(i)
                        
                        // Enforce configured host over whatever the backend reported
                        val rawUrl = obj.getString("url")
                        val newUrl = baseUrl + Uri.parse(rawUrl).path
                        
                        val subUrl = if (obj.has("subtitle_url") && !obj.isNull("subtitle_url")) baseUrl + Uri.parse(obj.getString("subtitle_url")).path else null
                        val audioUrl = if (obj.has("audio_url") && !obj.isNull("audio_url")) baseUrl + Uri.parse(obj.getString("audio_url")).path else null

                        files.add(FileItem(
                            obj.getString("name"), 
                            newUrl, 
                            obj.getString("type"),
                            obj.optString("hdr_type", ""),
                            subUrl,
                            audioUrl
                        ))
                    }
                    withContext(Dispatchers.Main) { adapter.submitList(files) }
                }
            } catch (e: Exception) { e.printStackTrace() }
        }
    }
}

class FileAdapter(private val onClick: (FileItem) -> Unit) : RecyclerView.Adapter<FileAdapter.ViewHolder>() {
    private var items = listOf<FileItem>()
    fun submitList(newItems: List<FileItem>) { items = newItems; notifyDataSetChanged() }
    class ViewHolder(view: View) : RecyclerView.ViewHolder(view) { val textView: TextView = view.findViewById(android.R.id.text1) }
    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): ViewHolder {
        val view = LayoutInflater.from(parent.context).inflate(android.R.layout.simple_list_item_1, parent, false)
        view.isFocusable = true; view.isFocusableInTouchMode = true; view.setBackgroundResource(R.drawable.item_selector)
        val textView = view.findViewById<TextView>(android.R.id.text1)
        textView.textSize = 22f; textView.setTextColor(android.graphics.Color.WHITE); textView.setPadding(48, 32, 48, 32)
        return ViewHolder(view)
    }
    override fun onBindViewHolder(holder: ViewHolder, position: Int) {
        val item = items[position]
        var label = item.name
        if (item.subtitleUrl != null) label += " [CC]"
        if (item.audioUrl != null) label += " [External Audio]"
        holder.textView.text = label
        holder.itemView.setOnClickListener { onClick(item) }
    }
    override fun getItemCount() = items.size
}
