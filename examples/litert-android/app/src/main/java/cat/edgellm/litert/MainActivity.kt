package cat.edgellm.litert

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Face
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.Icon
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import cat.edgellm.litert.llm.ChatScreen
import cat.edgellm.litert.ui.LiteRtTheme
import cat.edgellm.litert.vision.VisionScreen

/**
 * Single-activity Compose app demonstrating the two pillars of the LiteRT stack on-device:
 *   • "Vision"  — image classification with a `.tflite` model (LiteRT runtime).
 *   • "Chat"    — text generation with a Gemma `.task` bundle (LiteRT-LM).
 *
 * Built for the EDGE-LLM project as a minimal, readable proof that LiteRT runs entirely
 * on the device: private by default, works offline, no data leaves the phone.
 */
class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent {
            LiteRtTheme {
                var tab by rememberSaveable { mutableIntStateOf(0) }
                Scaffold(
                    bottomBar = {
                        NavigationBar {
                            NavigationBarItem(
                                selected = tab == 0,
                                onClick = { tab = 0 },
                                icon = { Icon(Icons.Filled.Search, contentDescription = null) },
                                label = { Text("Vision") },
                            )
                            NavigationBarItem(
                                selected = tab == 1,
                                onClick = { tab = 1 },
                                icon = { Icon(Icons.Filled.Face, contentDescription = null) },
                                label = { Text("Chat") },
                            )
                        }
                    },
                ) { innerPadding ->
                    Box(Modifier.fillMaxSize().padding(innerPadding)) {
                        when (tab) {
                            0 -> VisionScreen()
                            else -> ChatScreen()
                        }
                    }
                }
            }
        }
    }
}
