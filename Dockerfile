# Stage 1: Build Go Backend
FROM golang:1.23-alpine AS go-builder
WORKDIR /build
COPY backend/main.go .
RUN go mod init directstreamer && \
    go get github.com/gorilla/websocket && \
    go get github.com/fsnotify/fsnotify && \
    go build -ldflags="-s -w" -o server main.go

# Stage 2: Build Android TV APK
FROM ghcr.io/cirruslabs/android-sdk:36 AS android-builder
WORKDIR /android

ARG BACKEND_IP
ARG BACKEND_PORT
ARG SHOW_TOASTS
ARG FALLBACK
ENV BACKEND_IP=${BACKEND_IP}
ENV BACKEND_PORT=${BACKEND_PORT}
ENV SHOW_TOASTS=${SHOW_TOASTS}
ENV FALLBACK=${FALLBACK}

RUN apt-get update && apt-get install -y wget unzip && rm -rf /var/lib/apt/lists/*
RUN wget -q https://services.gradle.org/distributions/gradle-8.5-bin.zip && \
    unzip -q gradle-8.5-bin.zip -d /opt && \
    rm gradle-8.5-bin.zip
ENV PATH=${PATH}:/opt/gradle-8.5/bin
COPY frontend/ .
RUN yes | sdkmanager --licenses > /dev/null
RUN gradle assembleDebug --no-daemon

# Stage 3: Runtime Environment
FROM alpine:latest
WORKDIR /app
RUN apk add --no-cache ffmpeg android-tools grep
RUN mkdir -p /app/public
COPY --from=go-builder /build/server /app/server
COPY --from=android-builder /android/app/build/outputs/apk/debug/app-debug.apk /app/public/DirectStreamer.apk
COPY entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh
EXPOSE 8282
CMD ["/app/entrypoint.sh"]
