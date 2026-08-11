package cat.edgellm.litert.vision

import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.net.Uri
import android.provider.MediaStore
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import androidx.compose.runtime.rememberCoroutineScope

/**
 * UI for the LiteRT image-classification demo: pick a photo, run it through [ImageClassifier],
 * and render the top-5 labels with confidence bars. Inference runs off the main thread.
 */
@Composable
fun VisionScreen() {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()

    // The classifier is created lazily and reused. It owns native resources, so we keep one instance.
    val classifier = remember {
        runCatching { ImageClassifier(context) }
    }

    var bitmap by remember { mutableStateOf<Bitmap?>(null) }
    var results by remember { mutableStateOf<List<ImageClassifier.Recognition>>(emptyList()) }
    var inferenceMs by remember { mutableStateOf<Long?>(null) }
    var busy by remember { mutableStateOf(false) }

    val picker = rememberLauncherForActivityResult(ActivityResultContracts.GetContent()) { uri: Uri? ->
        uri ?: return@rememberLauncherForActivityResult
        val loaded = loadBitmap(context, uri)
        bitmap = loaded
        results = emptyList()
        inferenceMs = null
        val engine = classifier.getOrNull() ?: return@rememberLauncherForActivityResult
        if (loaded != null) {
            busy = true
            scope.launch {
                val start = System.nanoTime()
                val out = withContext(Dispatchers.Default) { engine.classify(loaded) }
                inferenceMs = (System.nanoTime() - start) / 1_000_000
                results = out
                busy = false
            }
        }
    }

    Column(
        Modifier.fillMaxWidth().padding(16.dp).verticalScroll(rememberScrollState()),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Text("LiteRT · On-device vision", style = MaterialTheme.typography.titleLarge)
        Text(
            "Classify a photo with a MobileNet .tflite model. Everything runs on the device — " +
                "no network, fully private.",
            style = MaterialTheme.typography.bodyMedium,
        )

        if (classifier.isFailure) {
            Card(Modifier.fillMaxWidth()) {
                Text(
                    "Model not loaded.\n\nAdd a MobileNet model to app/src/main/assets/ " +
                        "(see assets/MODELS.md). Expected: mobilenet_v1.tflite + labels_mobilenet.txt.",
                    Modifier.padding(16.dp),
                    style = MaterialTheme.typography.bodySmall,
                )
            }
        }

        Button(onClick = { picker.launch("image/*") }) { Text("Choose image") }

        bitmap?.let {
            Image(
                bitmap = it.asImageBitmap(),
                contentDescription = "Selected image",
                modifier = Modifier.fillMaxWidth().height(240.dp),
                contentScale = ContentScale.Fit,
            )
        }

        if (busy) LinearProgressIndicator(Modifier.fillMaxWidth())

        inferenceMs?.let { Text("Inference: ${it} ms", style = MaterialTheme.typography.labelLarge) }

        results.forEach { r ->
            Card(Modifier.fillMaxWidth()) {
                Column(Modifier.padding(12.dp)) {
                    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                        Text(r.label, style = MaterialTheme.typography.bodyLarge)
                        Text(
                            "${"%.1f".format(r.confidence * 100)}%",
                            fontFamily = FontFamily.Monospace,
                            style = MaterialTheme.typography.bodyLarge,
                        )
                    }
                    Spacer(Modifier.height(6.dp))
                    LinearProgressIndicator(
                        progress = { r.confidence.coerceIn(0f, 1f) },
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
            }
        }
    }
}

private fun loadBitmap(context: android.content.Context, uri: Uri): Bitmap? = runCatching {
    @Suppress("DEPRECATION")
    val raw = MediaStore.Images.Media.getBitmap(context.contentResolver, uri)
    // Models read RGB; ensure a software ARGB_8888 bitmap regardless of source format.
    raw.copy(Bitmap.Config.ARGB_8888, false)
}.getOrNull()
