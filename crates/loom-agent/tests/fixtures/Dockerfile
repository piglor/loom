FROM node:24-bookworm-slim
COPY container_codex.js /usr/local/bin/codex
RUN chmod 755 /usr/local/bin/codex
USER 1000:1000
WORKDIR /workspace
