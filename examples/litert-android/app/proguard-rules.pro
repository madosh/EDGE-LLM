# Keep LiteRT / TensorFlow Lite and MediaPipe GenAI classes (they use JNI + reflection).
-keep class org.tensorflow.lite.** { *; }
-keep class com.google.ai.edge.litert.** { *; }
-keep class com.google.mediapipe.** { *; }
-dontwarn org.tensorflow.lite.**
-dontwarn com.google.mediapipe.**
