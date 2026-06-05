package cat.edgellm.litert.llm

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp

/**
 * UI for the LiteRT-LM text-generation demo: type a prompt, stream a response from an
 * on-device Gemma model via [LlmEngine]. Shows the "edge LLM" half of the project on a phone.
 */
@Composable
fun ChatScreen() {
    val context = LocalContext.current

    // Engine load can fail if the model bundle hasn't been pushed yet; surface that clearly.
    val engineResult = remember { LlmEngine.tryCreate(context) }
    val engine = engineResult.getOrNull()

    DisposableEffect(Unit) {
        onDispose { engine?.close() }
    }

    var prompt by remember { mutableStateOf("Explain edge AI to a city mayor in two sentences.") }
    var output by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }

    Column(
        Modifier.fillMaxSize().padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Text("LiteRT-LM · On-device chat", style = MaterialTheme.typography.titleLarge)
        Text(
            "Generate text with a Gemma model running locally. No tokens leave the device.",
            style = MaterialTheme.typography.bodyMedium,
        )

        if (engine == null) {
            Card(Modifier.fillMaxWidth()) {
                Text(
                    "LLM model not found.\n\nPush a Gemma .task bundle to the device:\n" +
                        "  adb push gemma3-1b-it.task \\\n" +
                        "    /sdcard/Android/data/cat.edgellm.litert/files/gemma3-1b-it.task\n\n" +
                        "See assets/MODELS.md for where to download it.",
                    Modifier.padding(16.dp),
                    style = MaterialTheme.typography.bodySmall,
                )
            }
            return@Column
        }

        OutlinedTextField(
            value = prompt,
            onValueChange = { prompt = it },
            label = { Text("Prompt") },
            modifier = Modifier.fillMaxWidth(),
        )

        Button(
            enabled = !busy,
            onClick = {
                busy = true
                output = ""
                engine.reset()
                engine.generate(prompt) { partial, done ->
                    output += partial
                    if (done) busy = false
                }
            },
        ) { Text(if (busy) "Generating…" else "Generate") }

        Card(Modifier.fillMaxWidth()) {
            Text(
                output.ifEmpty { "Response will stream here…" },
                Modifier.padding(16.dp).verticalScroll(rememberScrollState()),
                style = MaterialTheme.typography.bodyLarge,
            )
        }
    }
}
