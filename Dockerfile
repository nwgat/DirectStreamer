# syntax=docker/dockerfile:1

# Stage 1: Build FFmpeg statically
FROM alpine:3.23 AS ffmpeg-builder
ENV PATH="/usr/lib/ccache:$PATH"
ENV CCACHE_DIR=/root/.ccache
RUN --mount=type=cache,target=/var/cache/apk \
    apk add --no-cache \
    build-base \
    yasm \
    nasm \
    git \
    coreutils \
    pkgconf \
    zlib-dev \
    zlib-static \
    xz-dev \
    xz-static \
    opus-dev \
    libxml2-dev \
    libxml2-static \
    ccache
WORKDIR /src
RUN git clone --depth 1 https://git.ffmpeg.org/ffmpeg.git
WORKDIR /src/ffmpeg
RUN --mount=type=cache,target=/root/.ccache \
    ./configure \
      --disable-everything \
      --disable-doc \
      --disable-network \
      --disable-autodetect \
      --enable-static \
      --pkg-config-flags="--static" \
      --extra-ldflags="-static" \
      --extra-libs="-lstdc++ -lm -lz -llzma" \
      --enable-libxml2 \
      --enable-protocol=file \
      --enable-filter=scale,aresample \
      --enable-demuxer=mp4,matroska,flac,ogg,dash \
      --enable-muxer=mp4,matroska,flac,ogg,opus,dash \
      --enable-encoder=aac,flac,opus,ac3,eac3,dca,truehd \
      --enable-parser=h264,hevc,aac,flac,opus,ac3,eac3,dca,truehd,av1 && \
    make clean && \
    make -j$(nproc) && \
    strip ffmpeg ffprobe

# Stage 2: Build Go Backend
FROM golang:1.23-alpine AS go-builder
WORKDIR /build
COPY backend/main.go .
RUN go mod init directstreamer && \
    go get github.com/gorilla/websocket && \
    go get github.com/fsnotify/fsnotify && \
    go build -ldflags="-s -w" -o server main.go

# Stage 3: Build Android TV APK
FROM ghcr.io/cirruslabs/android-sdk:36 AS android-builder
WORKDIR /android

ARG BACKEND_IP
ARG BACKEND_PORT
ARG RELEASE=yes
ENV BACKEND_IP=${BACKEND_IP}
ENV BACKEND_PORT=${BACKEND_PORT}
ENV RELEASE=${RELEASE}

RUN apt-get update && apt-get install -y wget unzip && rm -rf /var/lib/apt/lists/*
RUN wget -q https://services.gradle.org/distributions/gradle-8.5-bin.zip && \
    unzip -q gradle-8.5-bin.zip -d /opt && \
    rm gradle-8.5-bin.zip
ENV PATH=${PATH}:/opt/gradle-8.5/bin
COPY frontend/ .
RUN chmod +x build.sh
RUN yes | sdkmanager --licenses > /dev/null
RUN ./build.sh

# Stage 4: Runtime Environment
FROM alpine:latest
WORKDIR /app

# Install runtime dependencies and libraries required for official ADB
# Note: tzdata is added here to sync container time with host/env
# ADDED tini for process management and clean instant shutdowns!
RUN apk add --no-cache grep jq libgcc gcompat wget unzip tzdata tini

# Download and install official Android SDK platform-tools adb binary
RUN wget -q https://dl.google.com/android/repository/platform-tools-latest-linux.zip && \
    unzip -q platform-tools-latest-linux.zip platform-tools/adb && \
    install -m 755 platform-tools/adb /usr/local/bin/adb && \
    rm -rf platform-tools platform-tools-latest-linux.zip

RUN mkdir -p /app/public

# Copy custom static ffmpeg binaries from builder stage
COPY --from=ffmpeg-builder /src/ffmpeg/ffmpeg /usr/local/bin/ffmpeg
COPY --from=ffmpeg-builder /src/ffmpeg/ffprobe /usr/local/bin/ffprobe

COPY --from=go-builder /build/server /app/server
COPY --from=android-builder /android/app/build/outputs/apk/output.apk /app/public/DirectStreamer.apk

COPY adb_manager.sh /app/adb_manager.sh
RUN chmod +x /app/adb_manager.sh

EXPOSE 8282

# Start tini to pass signals to the process group, triggering instant shutdowns
ENTRYPOINT ["/sbin/tini", "-g", "--"]
