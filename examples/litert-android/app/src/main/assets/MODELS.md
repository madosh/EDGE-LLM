# Models

Model weights are **not** committed to git — they are large binaries. Add them locally
before running the app. The code degrades gracefully and shows setup hints if a model is missing.

## 1. Vision — MobileNet (`.tflite`, ~4–16 MB)

Drop two files into **this `assets/` folder**:

| File | What it is |
|------|------------|
| `mobilenet_v1.tflite` | A MobileNet image-classification model (float or quantized) |
| `labels_mobilenet.txt` | One label per line, matching the model's output order |

Where to get them:

- **LiteRT / Kaggle Models:** https://www.kaggle.com/models/google/mobilenet-v1 (download the `.tflite`)
- Or any ImageNet MobileNet/EfficientNet-Lite `.tflite` with a matching labels file.
- The classic 1001-line ImageNet labels file (background + 1000 classes) ships with most samples.

`ImageClassifier.kt` reads the input size and quantization type **from the model itself**, so a
float `224×224` or a quantized `uint8` MobileNet both work without code changes.

## 2. Chat — Gemma via LiteRT-LM (`.task` / `.litertlm`, ~500 MB–2 GB)

The LLM bundle is too big for the APK, so it is loaded from external app storage at runtime.

1. Download a ready-made bundle (accept the Gemma license first):
   - https://huggingface.co/litert-community  (look for `gemma3-1b-it` `.task` / `.litertlm`)
   - or the MediaPipe LLM samples model list.
2. Push it onto the device:

   ```bash
   adb push gemma3-1b-it.task \
     /sdcard/Android/data/cat.edgellm.litert/files/gemma3-1b-it.task
   ```

`LlmEngine.kt` looks for it at `getExternalFilesDir(null)/gemma3-1b-it.task`.

### Optional: convert + bundle your own model

Using the LiteRT / AI Edge Torch Generative API on a workstation:

```bash
pip install ai-edge-torch mediapipe
# 1. Convert & quantize a PyTorch/Keras LLM to .tflite
# 2. Bundle the .tflite + tokenizer into a .task with the MediaPipe bundler
```

See the LiteRT-LM docs for the current converter invocation.
