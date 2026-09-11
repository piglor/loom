FROM node:24-bookworm-slim@sha256:ba849c60be29959425b8734d57b8b4b7d56f98edd9504c9af091d5281095a71e AS source
ARG LOOM_BUILD_SHA=development
ARG LOOM_SOURCE_REF=local
ARG LOOM_SOURCE_REPOSITORY=https://github.com/piglor/loom.git
WORKDIR /source
COPY . .
RUN if [ "$LOOM_SOURCE_REF" != "local" ]; then \
      apt-get update && apt-get install -y --no-install-recommends git ca-certificates && rm -rf /var/lib/apt/lists/*; \
      rm -rf /tmp/loom-source; \
      git init /tmp/loom-source; \
      git -C /tmp/loom-source remote add origin "$LOOM_SOURCE_REPOSITORY"; \
      git -C /tmp/loom-source fetch --depth 1 origin "$LOOM_SOURCE_REF"; \
      git -C /tmp/loom-source checkout --detach FETCH_HEAD; \
      find /source -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +; \
      cp -a /tmp/loom-source/. /source/; \
    fi
RUN printf '{"build_sha":"%s","source_sha":"%s"}\n' "$LOOM_BUILD_SHA" "$(/source/scripts/source_fingerprint.sh)" > /source/build-manifest.json

FROM node:24-bookworm-slim@sha256:ba849c60be29959425b8734d57b8b4b7d56f98edd9504c9af091d5281095a71e AS web
ARG LOOM_BUILD_SHA=development
ENV VITE_BUILD_SHA=$LOOM_BUILD_SHA
WORKDIR /src
COPY --from=source /source/package.json /source/package-lock.json ./
COPY --from=source /source/apps/web/package.json apps/web/package.json
COPY --from=source /source/packages/client/package.json packages/client/package.json
RUN npm ci
COPY --from=source /source/apps/web/tsconfig.json /source/apps/web/index.html apps/web/
COPY --from=source /source/apps/web/src apps/web/src
COPY --from=source /source/packages/client/src packages/client/src
RUN npm run build

FROM golang:1.26-bookworm@sha256:9fdc884aacc3bec89b20ffc69f4bb369c78210e3e4f600387b5128b12c199f81 AS backend
WORKDIR /src
COPY services/loom/go.mod services/loom/go.sum ./
RUN go mod download
COPY services/loom ./
RUN CGO_ENABLED=0 go build -trimpath -o /loom-server .

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=backend /loom-server /loom-server
COPY --from=web /src/apps/web/dist /web
COPY --from=source /source/build-manifest.json /web/build-manifest.json
ENV LOOM_LISTEN_ADDR=0.0.0.0:8080 LOOM_WEB_DIR=/web
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/loom-server"]
