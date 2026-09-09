FROM node:24-bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && npm install --global @openai/codex@0.153.4
USER 1000:1000
WORKDIR /workspace
ENTRYPOINT ["/usr/bin/timeout", "--signal=KILL", "300", "codex", "app-server", "--stdio"]
