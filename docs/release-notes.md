## Go migration preview

This build replaces Rust/Tauri with a standard-library Go core, preserves the React interface, and adds native Kotlin/JNI Android packaging.

### Download the right file

- **Horizon-Manager-windows-x64.exe** — portable Windows 10/11 x64 application. Opens an embedded browser UI backed by an authenticated loopback-only Go host. Requires the desktop Java version appropriate to your Minecraft release.
- **Horizon-Manager-windows-arm64.exe** — same application for Windows on ARM64; use a compatible ARM64 desktop JVM.
- **Horizon-Manager-android-preview.apk** — sideloadable Android 8+ preview for ARM64 and x86-64. Contains the Kotlin UI and Go native libraries. Supports profiles, verified Vanilla downloads, and local backups. **Minecraft JVM hosting is unavailable on Android because an Android-compatible Java runtime is not included.** Start is disabled; the foreground service handles file/download jobs only.
- **SHA256SUMS.txt** — integrity hashes for the binary assets (not publisher authentication).

### Signing and data warnings

**Preview only.** Windows binaries are unsigned and may trigger SmartScreen. The APK is debug-signed, not a production-signed or Play Store release. CI builds can have different Android signing keys, so installing an updated preview may require uninstalling. **Uninstalling Android deletes app-private profiles and server files. Keep separate copies of important data; never use this preview as your only world backup.**

On Windows, use **Settings → Shut down local host** before exiting; closing the browser tab does not stop the Go host. Install Java separately and review Windows firewall consent before allowing LAN play. No firewall rules are changed automatically.

### Verification and remaining scope

Publication is gated on frontend compilation, Go tests with race detection, native Windows process tests, Windows cross-builds, Android JNI/APK compilation and lint, and Android-emulator smoke tests of the actual packaged UI. This does **not** establish physical-device compatibility or real Minecraft-world/JVM compatibility. Android hosting, automatic Java installation, plugin/mod catalogs, public tunnels, tray/firewall automation, and Launcher IPC are not implemented.

Source and commit provenance, checksums, and the successful workflow run are provided with this preview. No binaries or signing keys are checked into Git.
