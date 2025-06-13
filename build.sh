#!/bin/bash

APP_NAME="upload-go"
VERSION="v1.0.0"
BUILD_TIME=$(date -u '+%Y-%m-%dT%H:%M:%SZ')

echo "📦 Building $APP_NAME $VERSION at $BUILD_TIME..."

# MacOS
GOOS=darwin GOARCH=amd64 go build \
  -ldflags "-X main.Version=$VERSION -X main.BuildTime=$BUILD_TIME" \
  -o bin/${APP_NAME}_macos main.go db.go

# Windows
GOOS=windows GOARCH=amd64 go build \
  -ldflags "-X main.Version=$VERSION -X main.BuildTime=$BUILD_TIME" \
  -o bin/${APP_NAME}_win64.exe main.go db.go

# Linux
GOOS=linux GOARCH=amd64 go build \
  -ldflags "-X main.Version=$VERSION -X main.BuildTime=$BUILD_TIME" \
  -o bin/${APP_NAME}_linux main.go db.go

echo "✅ Build selesai: lihat file ${APP_NAME}_*.exe / .linux"
