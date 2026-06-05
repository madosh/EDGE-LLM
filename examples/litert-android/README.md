# LiteRT Edge — On-Device AI Demo (Android)

A minimal, readable Android app that runs **Google LiteRT** entirely on the device — no cloud,
no network, fully private. Built as a companion to the [EDGE-LLM](../../README.md) project to
demonstrate hands-on understanding of the LiteRT framework.

It shows **both** halves of the LiteRT stack:

| Tab | What it does | LiteRT component |
|-----|--------------|------------------|
| **Vision** | Classify a photo (MobileNet) | LiteRT runtime — `Interpreter` + GPU delegate |
| **Chat** | Generate text with Gemma | LiteRT-LM via the MediaPipe `tasks-genai` API |

---

## Why this matters (and why "on-device")

LiteRT (formerly **TensorFlow Lite**) is Google's runtime for running ML models *on the device
itself* — phone, tablet, kiosk, sensor — instead of a server. For a public administration this is
not a detail, it is the whole point:

- **Data sovereignty / GDPR** — citizen photos, text, and voice never leave the device. There is
  literally no `INTERNET` permission in this app.
- **Works offline** — a field worker in a valley with no coverage still gets inference.
- **Low cost & low latency** — no per-request cloud bill, no round-trip; results in milliseconds.
- **Sustainability** — compute happens on hardware that already exists.

These are exactly the properties that make edge AI attractive for the **Generalitat de Catalunya**
and Catalan public services (sanitat, atenció ciutadana, agricultura, mobilitat).

---

## How LiteRT works here (the part that proves understanding)

The demo deliberately uses the **low-level `Interpreter` API** for vision instead of a one-call
"task" wrapper, so the real mechanics are visible in
[`ImageClassifier.kt`](app/src/main/java/cat/edgellm/litert/vision/ImageClassifier.kt):

```
.tflite flatbuffer ──(memory-map)──▶ Interpreter ──(+ GpuDelegate)──▶ on-device graph
   Bitmap ──▶ resize 224×224 ──▶ normalize [-1,1] ──▶ input TensorImage
   Interpreter.run(input, output)
   output probability tensor ──▶ TensorLabel ──▶ top-5 labels
```

Things the code shows it understands:

1. **The framework is the runtime, not a model.** LiteRT loads a `.tflite` *flatbuffer* and
   executes its op graph. We memory-map it with `FileUtil.loadMappedFile` rather than reading bytes.
2. **Hardware acceleration is a delegate.** `CompatibilityList` + `GpuDelegate` push the graph onto
   the GPU/NPU when available, with a multi-threaded CPU (XNNPACK) fallback — one code path.
3. **Pre/post-processing must match the model.** The input size and quantization type are read
   *from the model's own tensors*, so a float `224×224` or a `uint8` quantized MobileNet both work.
4. **Packaging nuance.** LiteRT renamed the Maven coordinate `org.tensorflow:tensorflow-lite` →
   `com.google.ai.edge.litert:litert`, **but the code imports stayed `org.tensorflow.lite.*`** —
   migration is a one-line Gradle change. (See the comment block in `ImageClassifier.kt`.)

The **Chat** tab applies the same on-device philosophy to a small LLM (Gemma 3 1B) through
LiteRT-LM, streaming tokens locally — see
[`LlmEngine.kt`](app/src/main/java/cat/edgellm/litert/llm/LlmEngine.kt).

---

## Project layout

```
examples/litert-android/
├── build.gradle.kts            root build (AGP + Kotlin versions)
├── settings.gradle.kts
├── app/
│   ├── build.gradle.kts        LiteRT + tasks-genai + Compose dependencies
│   └── src/main/
│       ├── AndroidManifest.xml no INTERNET permission — on-device by design
│       ├── assets/MODELS.md    where to add the .tflite and Gemma .task files
│       └── java/cat/edgellm/litert/
│           ├── MainActivity.kt          two-tab Compose scaffold
│           ├── vision/ImageClassifier.kt  ← core LiteRT inference (Interpreter API)
│           ├── vision/VisionScreen.kt
│           ├── llm/LlmEngine.kt           ← LiteRT-LM text generation
│           ├── llm/ChatScreen.kt
│           └── ui/Theme.kt
└── docs/CONCEPTS.md            deeper LiteRT background
```

---

## Build & run

**Requirements:** Android Studio (Ladybug or newer), JDK 17, a device/emulator on API 26+.
A physical device is recommended for the GPU delegate and the LLM.

```bash
# 1. Open this folder in Android Studio (File ▸ Open ▸ examples/litert-android)
#    or generate the Gradle wrapper jar once:
gradle wrapper            # from examples/litert-android/

# 2. Add models — see app/src/main/assets/MODELS.md
#    Vision: drop mobilenet_v1.tflite + labels_mobilenet.txt into app/src/main/assets/
#    Chat:   adb push gemma3-1b-it.task to the device's external files dir

# 3. Build & install
./gradlew installDebug
```

The app runs without models — each tab shows clear setup instructions until you add them, so you
can install and explore the code first.

---

## How this relates to the EDGE-LLM control plane

The parent [EDGE-LLM / Cami Fleet](../../README.md) repo is the **fleet** side: a Go control plane
that deploys models to many edge devices and streams telemetry. That repo's agent talks to LiteRT
as a remote REST service. **This app is the device side made real** — the actual LiteRT runtime
loading a model and producing tokens/sec on hardware. Together they tell the full story:
*deploy a model to a fleet → run it on-device with LiteRT → stream the metrics back.*

---

## License

MIT — see [LICENSE](../../LICENSE).
