package cat.edgellm.litert.vision

import android.content.Context
import android.graphics.Bitmap
import org.tensorflow.lite.DataType
import org.tensorflow.lite.Interpreter
import org.tensorflow.lite.gpu.CompatibilityList
import org.tensorflow.lite.gpu.GpuDelegate
import org.tensorflow.lite.support.common.FileUtil
import org.tensorflow.lite.support.common.ops.NormalizeOp
import org.tensorflow.lite.support.image.ImageProcessor
import org.tensorflow.lite.support.image.TensorImage
import org.tensorflow.lite.support.image.ops.ResizeOp
import org.tensorflow.lite.support.label.TensorLabel
import org.tensorflow.lite.support.tensorbuffer.TensorBuffer
import java.io.Closeable

/**
 * On-device image classification with **LiteRT** (Google's successor to TensorFlow Lite).
 *
 * This class is the core of the demo: it shows the full LiteRT inference lifecycle, by hand,
 * without a high-level task wrapper — so the mechanics of the framework are visible:
 *
 *   1. Load a `.tflite` flatbuffer model from `assets/` into a memory-mapped [Interpreter].
 *   2. (Optionally) attach a [GpuDelegate] so the model runs on the device GPU/NPU.
 *   3. Pre-process the camera/gallery [Bitmap] into the model's input [TensorImage]
 *      (resize → normalize) using the LiteRT Support Library.
 *   4. Run a synchronous forward pass with [Interpreter.run].
 *   5. Map the output probability tensor to human-readable labels with [TensorLabel].
 *
 * Note on packages: LiteRT replaced the Maven coordinate
 * `org.tensorflow:tensorflow-lite` with `com.google.ai.edge.litert:litert`, but the **Java/Kotlin
 * import namespace deliberately stayed `org.tensorflow.lite.*`** — migrating an app is a
 * one-line Gradle change with zero code changes. That is why the imports above look "TFLite".
 */
class ImageClassifier(
    context: Context,
    private val modelPath: String = "mobilenet_v1.tflite",
    private val labelPath: String = "labels_mobilenet.txt",
    useGpu: Boolean = true,
    private val maxResults: Int = 5,
) : Closeable {

    private val interpreter: Interpreter
    private val labels: List<String>
    private var gpuDelegate: GpuDelegate? = null

    /** Input image side length (MobileNet v1 = 224×224), read from the model itself. */
    private val inputSize: Int

    /** Whether the model expects float32 (normalized) or uint8 (quantized) input. */
    private val isQuantized: Boolean

    init {
        // ── 1. Build interpreter options, enabling GPU acceleration when supported. ──
        val options = Interpreter.Options().apply {
            val compatList = CompatibilityList()
            if (useGpu && compatList.isDelegateSupportedOnThisDevice) {
                // GPU/NPU path — common on Pixel, Qualcomm Adreno, MediaTek, Mali devices.
                gpuDelegate = GpuDelegate(compatList.bestOptionsForThisDevice)
                addDelegate(gpuDelegate)
            } else {
                // CPU fallback — XNNPACK multi-threaded kernels.
                numThreads = 4
            }
        }

        // ── 2. Memory-map the flatbuffer model and create the interpreter. ──
        val modelBuffer = FileUtil.loadMappedFile(context, modelPath)
        interpreter = Interpreter(modelBuffer, options)
        labels = FileUtil.loadLabels(context, labelPath)

        // ── 3. Inspect the input tensor so pre-processing matches the model exactly. ──
        val inputTensor = interpreter.getInputTensor(0)
        inputSize = inputTensor.shape()[1] // shape = [1, H, W, 3]
        isQuantized = inputTensor.dataType() == DataType.UINT8
    }

    /** Result of one inference: a label and its probability in [0, 1]. */
    data class Recognition(val label: String, val confidence: Float)

    /** Classify a single bitmap and return the top-[maxResults] labels, best first. */
    fun classify(bitmap: Bitmap): List<Recognition> {
        // ── Pre-process: resize to the model input and normalize pixel values. ──
        // Float models expect (pixel - 127.5) / 127.5 → [-1, 1]; quantized models keep raw uint8.
        val imageProcessor = ImageProcessor.Builder()
            .add(ResizeOp(inputSize, inputSize, ResizeOp.ResizeMethod.BILINEAR))
            .apply {
                if (!isQuantized) add(NormalizeOp(127.5f, 127.5f))
            }
            .build()

        val tensorImage = TensorImage(if (isQuantized) DataType.UINT8 else DataType.FLOAT32)
        tensorImage.load(bitmap)
        val processedImage = imageProcessor.process(tensorImage)

        // ── Allocate the output buffer to match the model's output tensor. ──
        val outputTensor = interpreter.getOutputTensor(0)
        val outputBuffer = TensorBuffer.createFixedSize(outputTensor.shape(), outputTensor.dataType())

        // ── 4. The actual on-device forward pass. Everything stays on the device. ──
        interpreter.run(processedImage.buffer, outputBuffer.buffer.rewind())

        // ── 5. Dequantize/normalize scores and join them to labels. ──
        val probabilityProcessor = if (isQuantized) {
            // Quantized output: scale uint8 [0,255] back to a probability via the model's affine params.
            org.tensorflow.lite.support.common.TensorProcessor.Builder()
                .add(NormalizeOp(0f, 255f))
                .build()
        } else {
            org.tensorflow.lite.support.common.TensorProcessor.Builder().build()
        }
        val labeled = TensorLabel(labels, probabilityProcessor.process(outputBuffer))

        return labeled.mapWithFloatValue
            .map { (label, score) -> Recognition(label, score) }
            .sortedByDescending { it.confidence }
            .take(maxResults)
    }

    override fun close() {
        interpreter.close()
        gpuDelegate?.close()
        gpuDelegate = null
    }
}
