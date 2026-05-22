package com.example.tvstream

import android.content.Intent
import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
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

data class FileItem(val name: String, val url: String, val type: String)

class MainActivity : AppCompatActivity() {
	private lateinit var recyclerView: RecyclerView
	private val client = OkHttpClient()
	private val adapter = FileAdapter { fileItem ->
		val intent = Intent(this, PlayerActivity::class.java).apply {
			putExtra("URL", fileItem.url)
		}
		startActivity(intent)
	}

	override fun onCreate(savedInstanceState: Bundle?) {
		super.onCreate(savedInstanceState)
		setContentView(R.layout.activity_main)

		recyclerView = findViewById(R.id.recyclerView)
		recyclerView.layoutManager = LinearLayoutManager(this)
		recyclerView.adapter = adapter

		val backendUrl = BuildConfig.BACKEND_URL
		fetchFiles("$backendUrl/api/files")
	}

	private fun fetchFiles(urlStr: String) {
		lifecycleScope.launch(Dispatchers.IO) {
			try {
				val request = Request.Builder().url(urlStr).build()
				client.newCall(request).execute().use { response ->
					if (!response.isSuccessful) return@use
					
					val responseData = response.body?.string() ?: return@use
					val jsonArray = JSONArray(responseData)
					val files = mutableListOf<FileItem>()
					
					for (i in 0 until jsonArray.length()) {
						val obj = jsonArray.getJSONObject(i)
						files.add(FileItem(obj.getString("name"), obj.getString("url"), obj.getString("type")))
					}
					
					withContext(Dispatchers.Main) {
						adapter.submitList(files)
					}
				}
			} catch (e: Exception) {
				e.printStackTrace()
			}
		}
	}
}

class FileAdapter(private val onClick: (FileItem) -> Unit) : RecyclerView.Adapter<FileAdapter.ViewHolder>() {
	private var items = listOf<FileItem>()
	
	fun submitList(newItems: List<FileItem>) {
		items = newItems
		notifyDataSetChanged()
	}

	class ViewHolder(view: View) : RecyclerView.ViewHolder(view) {
		val textView: TextView = view.findViewById(android.R.id.text1)
	}

	override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): ViewHolder {
		val view = LayoutInflater.from(parent.context).inflate(android.R.layout.simple_list_item_1, parent, false)
		view.isFocusable = true
		view.isFocusableInTouchMode = true
		view.setBackgroundResource(android.R.drawable.list_selector_background)
		
		val textView = view.findViewById<TextView>(android.R.id.text1)
		textView.textSize = 22f
		textView.setTextColor(android.graphics.Color.WHITE)
		textView.setPadding(48, 32, 48, 32)
		
		return ViewHolder(view)
	}

	override fun onBindViewHolder(holder: ViewHolder, position: Int) {
		val item = items[position]
		holder.textView.text = item.name
		holder.itemView.setOnClickListener { onClick(item) }
	}

	override fun getItemCount() = items.size
}
