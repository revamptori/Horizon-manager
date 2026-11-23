#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
: "${ANDROID_NDK_HOME:?Set ANDROID_NDK_HOME to Android NDK 28.0.13004108}"
prebuilt="$ANDROID_NDK_HOME/toolchains/llvm/prebuilt/linux-x86_64/bin"
for target in 'arm64 arm64-v8a aarch64-linux-android26-clang' 'amd64 x86_64 x86_64-linux-android26-clang'; do
  read -r arch abi compiler <<< "$target"
  mkdir -p "android/app/src/main/jniLibs/$abi"
  GOOS=android GOARCH="$arch" CGO_ENABLED=1 CC="$prebuilt/$compiler" \
    go build -trimpath -buildmode=c-shared -ldflags='-s -w' -o "android/app/src/main/jniLibs/$abi/libhorizon.so" ./cmd/android
  rm -f "android/app/src/main/jniLibs/$abi/libhorizon.h"
done
gradle -p android --no-daemon :app:assembleDebug :app:lintDebug
mkdir -p release
cp android/app/build/outputs/apk/debug/app-debug.apk release/Horizon-Manager-android-preview.apk
