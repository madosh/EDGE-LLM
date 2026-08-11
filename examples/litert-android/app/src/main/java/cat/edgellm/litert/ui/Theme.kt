package cat.edgellm.litert.ui

import android.os.Build
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.dynamicDarkColorScheme
import androidx.compose.material3.dynamicLightColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext

// EDGE-LLM accent — a Catalan-flag inspired red/yellow on a calm neutral surface.
private val Light = lightColorScheme(
    primary = Color(0xFFC8102E),
    secondary = Color(0xFFF1BF00),
)
private val Dark = darkColorScheme(
    primary = Color(0xFFFF5A6E),
    secondary = Color(0xFFF1BF00),
)

@Composable
fun LiteRtTheme(content: @Composable () -> Unit) {
    val dark = isSystemInDarkTheme()
    val colors = when {
        Build.VERSION.SDK_INT >= Build.VERSION_CODES.S -> {
            val ctx = LocalContext.current
            if (dark) dynamicDarkColorScheme(ctx) else dynamicLightColorScheme(ctx)
        }
        dark -> Dark
        else -> Light
    }
    MaterialTheme(colorScheme = colors, content = content)
}
