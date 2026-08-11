plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "cat.edgellm.litert"
    compileSdk = 35

    defaultConfig {
        applicationId = "cat.edgellm.litert"
        minSdk = 26 // LiteRT GPU delegate + tasks-genai need a reasonably modern device.
        targetSdk = 35
        versionCode = 1
        versionName = "1.0"
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
        }
    }

    // Model flatbuffers are already compressed; don't let aapt re-compress them.
    androidResources {
        noCompress += listOf("tflite", "task", "litertlm")
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions {
        jvmTarget = "17"
    }
    buildFeatures {
        compose = true
    }
    composeOptions {
        kotlinCompilerExtensionVersion = "1.5.15"
    }
}

dependencies {
    // ── LiteRT runtime (successor to TensorFlow Lite) ───────────────────────────
    // NOTE: the Maven group is `com.google.ai.edge.litert`, but code imports stay
    // `org.tensorflow.lite.*` — migrating from TFLite is a one-line dependency change.
    implementation("com.google.ai.edge.litert:litert:2.1.0")            // core interpreter
    implementation("com.google.ai.edge.litert:litert-gpu:2.1.0")        // GPU/NPU delegate
    implementation("com.google.ai.edge.litert:litert-support:1.4.0")    // TensorImage / labels

    // ── LiteRT-LM via the MediaPipe GenAI LLM Inference API ─────────────────────
    implementation("com.google.mediapipe:tasks-genai:0.10.27")

    // ── Jetpack Compose UI ──────────────────────────────────────────────────────
    implementation(platform("androidx.compose:compose-bom:2025.01.00"))
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.material:material-icons-extended")
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.ui:ui-tooling-preview")
    debugImplementation("androidx.compose.ui:ui-tooling")
    implementation("androidx.activity:activity-compose:1.9.3")
    implementation("androidx.core:core-ktx:1.15.0")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.9.0")
}
