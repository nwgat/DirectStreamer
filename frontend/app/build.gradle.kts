plugins {
	id("com.android.application")
	id("org.jetbrains.kotlin.android")
}
android {
	namespace = "com.example.tvstream"
	compileSdk = 36

	buildFeatures {
		buildConfig = true
	}

	defaultConfig {
		applicationId = "com.example.tvstream"
		minSdk = 26
		targetSdk = 36
		versionCode = 1
		versionName = "1.0"

		val backendIp = System.getenv("BACKEND_IP") ?: "127.0.0.1"
		val backendPort = System.getenv("BACKEND_PORT") ?: "8282"
		val showToasts = System.getenv("SHOW_TOASTS") ?: "yes"
		val fallback = System.getenv("FALLBACK") ?: "yes"
		
		buildConfigField("String", "BACKEND_URL", "\"http://${backendIp}:${backendPort}\"")
		buildConfigField("boolean", "SHOW_TOASTS", if (showToasts == "yes") "true" else "false")
		buildConfigField("boolean", "FALLBACK", if (fallback == "yes") "true" else "false")
	}

	buildTypes {
		release {
			isMinifyEnabled = false
			proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
		}
	}
	compileOptions {
		sourceCompatibility = JavaVersion.VERSION_1_8
		targetCompatibility = JavaVersion.VERSION_1_8
	}
	kotlinOptions {
		jvmTarget = "1.8"
	}
}
dependencies {
	implementation("androidx.core:core-ktx:1.12.0")
	implementation("androidx.appcompat:appcompat:1.6.1")
	implementation("androidx.recyclerview:recyclerview:1.3.2")
	val media3Version = "1.3.0"
	implementation("androidx.media3:media3-exoplayer:$media3Version")
	implementation("androidx.media3:media3-exoplayer-dash:$media3Version")
	implementation("androidx.media3:media3-ui:$media3Version")
	
	implementation("com.squareup.okhttp3:okhttp:4.12.0")
	implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.7.3")
	implementation("androidx.lifecycle:lifecycle-runtime-ktx:2.6.2")
}
