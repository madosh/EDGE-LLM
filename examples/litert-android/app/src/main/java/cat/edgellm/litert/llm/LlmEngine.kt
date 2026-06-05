package cat.edgellm.litert.llm

import android.content.Context
import com.google.mediapipe.tasks.genai.llminference.LlmInference
import com.google.mediapipe.tasks.genai.llminference.LlmInference.LlmInferenceOptions
import com.google.mediapipe.tasks.genai.llminference.LlmInferenceSession
import com.google.mediapipe.tasks.genai.llminference.LlmInferenceSession.LlmInferenceSessionOptions
import java.io.Closeable
import java.io.File

/**
 * On-device LLM text generation with **LiteRT-LM**, the generative side of the LiteRT stack,
 * accessed through the MediaPipe `tasks-genai` LLM Inference API.
 *
 * This is the "extension" of the vision demo: the same on-device philosophy (no cloud, private,
 * offline-capable) applied to a small language model such as **Gemma 3 1B** packaged as a
 * `.task` / `.litertlm` bundle.
 *
 * Pipeline:
 *   1. Point [LlmInference] at a model bundle on local storage and pick a backend (GPU/CPU).
 *   2. Open an [LlmInferenceSession] with sampling parameters (topK, temperature).
 *   3. Feed the prompt as a query chunk and stream tokens back as they are produced.
 *
 * The model file is NOT bundled in the APK (it is hundreds of MB). It is expected at
 * [modelFile]; see `assets/MODELS.md` for how to push a Gemma bundle onto the device.
 */
class LlmEngine private constructor(
    private val llmInference: LlmInference,
) : Closeable {

    private var session: LlmInferenceSession = newSession()

    private fun newSession(): LlmInferenceSession =
        LlmInferenceSession.createFromOptions(
            llmInference,
            LlmInferenceSessionOptions.builder()
                .setTopK(40)
                .setTopP(0.95f)
                .setTemperature(0.8f)
                .build(),
        )

    /**
     * Stream a response to [prompt]. [onPartial] is called with each incremental chunk;
     * the boolean is `true` on the final chunk. All computation happens on-device.
     */
    fun generate(prompt: String, onPartial: (String, Boolean) -> Unit) {
        session.addQueryChunk(prompt)
        session.generateResponseAsync { partialResult, done ->
            onPartial(partialResult, done)
        }
    }

    /** Reset conversation state for a fresh turn. */
    fun reset() {
        session.close()
        session = newSession()
    }

    override fun close() {
        session.close()
        llmInference.close()
    }

    companion object {
        /** Default location to push the model bundle: `adb push gemma.task <this path>`. */
        fun defaultModelFile(context: Context): File =
            File(context.getExternalFilesDir(null), "gemma3-1b-it.task")

        /**
         * Load the engine, or return `null` with a reason if the model bundle is missing —
         * so the UI can show clear setup instructions instead of crashing.
         */
        fun tryCreate(context: Context, modelFile: File = defaultModelFile(context)): Result<LlmEngine> {
            if (!modelFile.exists()) {
                return Result.failure(
                    IllegalStateException("Model bundle not found at ${modelFile.absolutePath}"),
                )
            }
            return runCatching {
                val options = LlmInferenceOptions.builder()
                    .setModelPath(modelFile.absolutePath)
                    .setMaxTokens(1024)
                    // GPU backend gives the best tokens/sec on most modern phones; the API
                    // transparently falls back where unsupported.
                    .setPreferredBackend(LlmInference.Backend.GPU)
                    .build()
                LlmEngine(LlmInference.createFromOptions(context, options))
            }
        }
    }
}
