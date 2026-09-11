FROM node:24-bookworm-slim@sha256:ba849c60be29959425b8734d57b8b4b7d56f98edd9504c9af091d5281095a71e AS web
ARG VITE_BUILD_SHA=development
ENV VITE_BUILD_SHA=$VITE_BUILD_SHA
WORKDIR /src
COPY package.json package-lock.json ./
COPY apps/web/package.json apps/web/package.json
COPY packages/client/package.json packages/client/package.json
RUN npm ci
COPY apps/web/tsconfig.json apps/web/index.html apps/web/
COPY apps/web/src apps/web/src
COPY packages/client/src packages/client/src
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
ENV LOOM_LISTEN_ADDR=0.0.0.0:8080 LOOM_WEB_DIR=/web
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/loom-server"]
