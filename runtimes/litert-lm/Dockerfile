# LiteRT-LM's OpenAI-compatible server (`litert-lm serve`), for the "litert"
# compose profile. On a real edge device, install the CLI natively instead
# (`uv tool install litert-lm`) and run `litert-lm serve`.
FROM python:3.12-slim

ARG LITERT_LM_VERSION=">=0.17,<0.18"
RUN pip install --no-cache-dir "litert-lm${LITERT_LM_VERSION}"

EXPOSE 9379
# Only reachable on the compose network; nothing publishes this port.
ENTRYPOINT ["litert-lm", "serve", "--host", "0.0.0.0", "--port", "9379"]
