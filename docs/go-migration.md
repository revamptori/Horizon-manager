# Go migration and downloadable preview plan

## Architecture and acceptance criteria

- Replace the Rust/Tauri host with a standard-library Go core; keep the existing React UI and command contract behind a typed platform bridge.
- Windows: ship a portable, self-contained EXE with embedded UI assets. It opens a browser UI backed by an authenticated loopback-only host, never a LAN control endpoint. Persist profiles independently of the temporary browser origin. Use native process control and graceful shutdown.
- Android: ship an installable Kotlin/WebView app with a Go shared library reached through JNI, not an exposed HTTP server. Restrict the WebView bridge to packaged assets. Use a foreground data-sync service, notification, and bounded wake lock for long file/download operations. Do not use desktop Java on Android or claim server hosting without a compatible Android JVM.
- Preserve explicit EULA consent, official metadata and checksum verification, RFC 1918 binding defaults, offline backups, and non-destructive archive restore defenses. Add executable tests for the Go port.
- Build Windows and Android artifacts in CI, publish a clearly labeled public preview with checksums, and document signing, platform limitations, and reproducible build instructions. Keep generated binaries/SDKs out of Git.
- Make commits using the repository's existing authenticated user's Git identity. Do not change the session branch.

## Milestones

1. Inspect existing architecture/toolchains and confirm authorship (complete).
2. Port commands, process lifecycle, networking, verified downloads, backups and tests to Go.
3. Replace Tauri frontend calls; add durable host preferences and honest platform capabilities.
4. Package a Windows launcher and a Kotlin Android app with the shared Go core.
5. Add CI, run builds/tests, commit/push this branch, and publish public preview downloads if the GitHub connection permits releases/workflows.

## Signing / runtime boundary

The preview EXE is unsigned (Windows SmartScreen may warn). An Android debug APK is installable but is not a production-signed release; builds may have different signing keys, so in-place updates are not guaranteed. A stable production signing key must be managed outside source control. The Android preview manages local files/profiles and verified Vanilla downloads/backups, but **Minecraft JVM hosting remains unavailable** until an Android-compatible runtime is integrated and tested. This distinction must appear in the UI and release notes, not only here.
