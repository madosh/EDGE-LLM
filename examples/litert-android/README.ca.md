# LiteRT Edge — Demostració d'IA al Dispositiu (Android)

Una aplicació Android mínima i llegible que executa **Google LiteRT** completament al
dispositiu — sense núvol, sense xarxa, totalment privada. Creada com a complement del projecte
[EDGE-LLM](../../README.md) per demostrar el domini pràctic del framework LiteRT.

> 🌐 *Read this in [English](README.md).*

Mostra **les dues meitats** de l'stack LiteRT:

| Pestanya | Què fa | Component LiteRT |
|----------|--------|------------------|
| **Visió** | Classifica una foto (MobileNet) | Motor LiteRT — `Interpreter` + delegat de GPU |
| **Xat** | Genera text amb Gemma | LiteRT-LM via l'API MediaPipe `tasks-genai` |

---

## Per què és important (i per què "al dispositiu")

LiteRT (abans **TensorFlow Lite**) és el motor de Google per executar models d'IA *al mateix
dispositiu* — telèfon, tauleta, quiosc, sensor — en lloc d'un servidor. Per a una administració
pública, això no és un detall, és tot el sentit:

- **Sobirania de dades / RGPD** — les fotos, els textos i la veu de la ciutadania mai surten del
  dispositiu. Aquesta aplicació literalment no té permís d'`INTERNET`.
- **Funciona sense connexió** — un treballador de camp en una vall sense cobertura segueix obtenint
  inferència.
- **Cost i latència baixos** — sense factura del núvol per petició, sense anada i tornada;
  resultats en mil·lisegons.
- **Sostenibilitat** — la computació passa sobre maquinari que ja existeix.

Són exactament les propietats que fan atractiva la IA a l'edge per a la **Generalitat de
Catalunya** i els serveis públics catalans (sanitat, atenció ciutadana, agricultura, mobilitat).

---

## Com funciona LiteRT aquí (la part que demostra la comprensió)

La demostració utilitza deliberadament l'**API de baix nivell `Interpreter`** per a la visió en
lloc d'un embolcall d'alt nivell, de manera que la mecànica real queda visible a
[`ImageClassifier.kt`](app/src/main/java/cat/edgellm/litert/vision/ImageClassifier.kt):

```
flatbuffer .tflite ──(mapat a memòria)──▶ Interpreter ──(+ GpuDelegate)──▶ graf al dispositiu
   Bitmap ──▶ redimensionar 224×224 ──▶ normalitzar [-1,1] ──▶ TensorImage d'entrada
   Interpreter.run(entrada, sortida)
   tensor de probabilitats ──▶ TensorLabel ──▶ 5 etiquetes principals
```

El que el codi demostra que entén:

1. **El framework és el motor, no un model.** LiteRT carrega un *flatbuffer* `.tflite` i executa el
   seu graf d'operacions. El mapem a memòria amb `FileUtil.loadMappedFile`.
2. **L'acceleració per maquinari és un delegat.** `CompatibilityList` + `GpuDelegate` porten el graf
   a la GPU/NPU quan està disponible, amb retorn a CPU multifil (XNNPACK) — un sol camí de codi.
3. **El pre/postprocessament ha de coincidir amb el model.** La mida d'entrada i el tipus de
   quantització es llegeixen *dels propis tensors del model*, així models float i `uint8` funcionen.
4. **Detall d'empaquetatge.** LiteRT va canviar la coordenada Maven `org.tensorflow:tensorflow-lite`
   → `com.google.ai.edge.litert:litert`, **però els imports del codi segueixen sent
   `org.tensorflow.lite.*`** — la migració és un canvi d'una línia al Gradle.

La pestanya **Xat** aplica la mateixa filosofia a un LLM petit (Gemma 3 1B) mitjançant LiteRT-LM,
transmetent tokens localment — vegeu
[`LlmEngine.kt`](app/src/main/java/cat/edgellm/litert/llm/LlmEngine.kt).

---

## Compilació i execució

**Requisits:** Android Studio (Ladybug o superior), JDK 17, un dispositiu/emulador amb API 26+.
Es recomana un dispositiu físic per al delegat de GPU i per a l'LLM.

```bash
# 1. Obriu aquesta carpeta a Android Studio (File ▸ Open ▸ examples/litert-android)
#    o genereu el wrapper de Gradle un cop:
gradle wrapper            # des de examples/litert-android/

# 2. Afegiu models — vegeu app/src/main/assets/MODELS.md
#    Visió: poseu mobilenet_v1.tflite + labels_mobilenet.txt a app/src/main/assets/
#    Xat:   adb push gemma3-1b-it.task al directori de fitxers externs del dispositiu

# 3. Compileu i instal·leu
./gradlew installDebug
```

L'aplicació funciona sense models — cada pestanya mostra instruccions de configuració clares fins
que n'afegiu, així podeu instal·lar-la i explorar el codi primer.

---

## Casos d'ús per al sector públic

- **Sanitat** — triatge al dispositiu i precribratge d'imatges on la privadesa és crítica.
- **Atenció ciutadana** — classificació de documents/fotos sense connexió i assistent local en català.
- **Agricultura i medi ambient** — reconeixement de cultius, plagues i espècies al camp sense senyal.
- **Mobilitat** — percepció al dispositiu de baixa latència sense transmetre vídeo a un servidor.

---

## Relació amb el pla de control EDGE-LLM

El repositori pare [EDGE-LLM / Cami Fleet](../../README.md) és el costat de la **flota**: un pla de
control en Go que desplega models a molts dispositius edge i transmet telemetria. **Aquesta
aplicació és el costat del dispositiu fet realitat** — el motor LiteRT real carregant un model i
produint tokens/segon sobre maquinari. Junts expliquen la història completa:
*desplegar un model a una flota → executar-lo al dispositiu amb LiteRT → retornar les mètriques.*

---

## Llicència

MIT — vegeu [LICENSE](../../LICENSE).
