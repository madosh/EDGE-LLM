# LiteRT concepts — a short field guide

A condensed reference behind the demo, written to show *why* each piece exists.

## What LiteRT is

**LiteRT** ("Lite Runtime") is Google's on-device ML runtime, the successor to **TensorFlow Lite**.
It runs trained models directly on edge hardware (Android, embedded Linux, microcontrollers, iOS)
with a tiny footprint. The pipeline is:

```
Train (TF / PyTorch / JAX)
        │  convert + quantize
        ▼
   model.tflite           ← a FlatBuffer: graph + weights, self-describing
        │  load
        ▼
   LiteRT Interpreter / CompiledModel   ← executes the graph
        │  delegate
        ▼
   CPU (XNNPACK) · GPU · NPU / DSP      ← hardware acceleration
```

## Key ideas the demo exercises

| Concept | Meaning | Where in the demo |
|---------|---------|-------------------|
| **FlatBuffer model** | `.tflite` is a zero-copy, memory-mappable container of the op graph + weights | `FileUtil.loadMappedFile(...)` |
| **Interpreter** | Loads the graph and runs `run(input, output)` forward passes | `ImageClassifier.interpreter` |
| **Delegate** | Off-loads the graph to GPU/NPU; CPU fallback otherwise | `GpuDelegate` + `CompatibilityList` |
| **Quantization** | int8/uint8 weights → smaller & faster, slight accuracy cost | input `DataType` check |
| **Tensors & shapes** | I/O are fixed-shape tensors; pre-processing must match exactly | `getInputTensor(0).shape()` |
| **Support Library** | Helpers for image/label tensor plumbing (`TensorImage`, `TensorLabel`) | pre/post-processing |
| **LiteRT-LM** | The generative branch: run small LLMs (Gemma) on-device | `LlmEngine` / `tasks-genai` |

## Two APIs, one runtime

- **Interpreter API** — the classic, fully-supported path (used here for clarity). Migrating from
  TFLite is just swapping the Gradle dependency; imports stay `org.tensorflow.lite.*`.
- **CompiledModel API (LiteRT Next)** — the newer high-performance interface that streamlines
  CPU/GPU/NPU acceleration. Same model files; a natural next step from this demo.

## Why edge AI for the public sector

- **Privacy by construction** — inference where the data is born; nothing to transmit or store.
- **Resilience** — no dependency on connectivity or a cloud provider's uptime.
- **Cost** — capital hardware instead of recurring inference bills.
- **Equity** — works the same in a connected city and a remote comarca.

## Further reading

- LiteRT overview — https://ai.google.dev/edge/litert/overview
- LiteRT for Android — https://ai.google.dev/edge/litert/android
- LiteRT-LM on Android — https://ai.google.dev/edge/litert-lm/android
- LLM Inference (MediaPipe) — https://ai.google.dev/edge/mediapipe/solutions/genai/llm_inference/android
