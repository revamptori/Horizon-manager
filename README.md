# Horizon Server Manager

A local-first Minecraft server manager with a React/TypeScript interface, a **Go core**, a portable Windows launcher, and a Kotlin Android application. Rust and Tauri are no longer required.

## Downloads

Installable preview assets are published on the [GitHub Releases page](https://github.com/revamptori/Horizon-manager/releases). Use an actual release asset, not GitHub's automatically generated source ZIP.

| Asset | Requirements / behavior |
| --- | --- |
| `Horizon-Manager-windows-x64.exe` | 64-bit Windows 10/11, a modern browser, and the desktop Java version required by the chosen Minecraft release. Portable, no installer or admin privileges required. |
| `Horizon-Manager-windows-arm64.exe` | Windows on ARM64, a modern browser, and a compatible ARM64 desktop JVM. |
| `Horizon-Manager-android-preview.apk` | Android 8+ (API 26), ARM64 or x86-64, and a recent Android System WebView. **File/download manager only; Minecraft JVM hosting is not available.** |
| `SHA256SUMS.txt` | SHA-256 hashes of the downloadable binaries. |

These are **preview builds**, not production-signed applications. The EXEs are unsigned and Windows SmartScreen may warn. The APK is debug-signed, sideloaded rather than Play Store distributed, and may require uninstalling an earlier preview because CI signing keys can change. **Android uninstall deletes app-private server files and profiles.** Do not use the preview to store your only copy of a world. Never bypass a security warning unless you have checked the source and trust this build. Publishing a checksum is integrity metadata, not publisher authentication.

## What works / platform boundary

- Responsive dark/light UI, durable local profiles/preferences, and local activity history.
- Official Vanilla release lookup, metadata and JAR SHA-1 validation, bounded streaming downloads, and explicit Minecraft EULA consent.
- Private RFC 1918 IPv4 detection, port availability checks, and deliberate all-interface opt-in. CGNAT `100.64.0.0/10` is not treated as private LAN space. `0.0.0.0` is a bind address, not a player connection address.
- **Desktop**: Java process launch without shell interpolation, streamed output, single-line console commands, crash/exit status, graceful stop with a bounded forced-stop fallback, and native Windows Job Object containment.
- Offline ZIP backups and guarded restoration to a **new sibling directory**. Existing worlds are not overwritten. Traversal, links, duplicate/case-colliding paths, Windows device/ADS names, entry counts, checksums, and expanded-size limits are checked.
- **Android**: the same Go file/download/backup core through JNI; an asset-only Kotlin WebView; foreground data-sync service, notification, bounded wake lock, and network-change notifications for real local/download jobs. No HTTP control listener is created on Android.

**Android does not bundle an Android-compatible JVM. Desktop Java binaries cannot run there.** Start is deliberately disabled and the core rejects hosting requests. The foreground service handles downloads/archives only; it is not a Minecraft server service. A compatible JVM and device/lifecycle testing are required before Android hosting can be enabled.

Other distributions, package catalogs, Java installers, public tunnels, firewall/tray automation, metrics, and Launcher IPC remain unavailable. A normal public Cloudflare HTTP tunnel does not expose Minecraft Java TCP to arbitrary players. No public connectivity is claimed.

## Using the Windows preview

1. Download the EXE matching your CPU and verify it against the release's checksum file.
2. Install the appropriate desktop JVM and make `java` available on `PATH` (recent Minecraft releases commonly require Java 21; check the selected release).
3. Run the EXE. It opens its embedded interface in your default browser. The application does **not** need a separate Node, Go, or Rust installation.
4. Create an instance in a new empty folder, select a private network interface, review the EULA, and install.
5. Use **Settings → Shut down local host** to stop managed servers and exit. Closing the browser tab alone does not stop the host. Only one host can use a given data directory. If you lose the only tab, end the Horizon process before relaunching; this may force-stop a running server, so use the in-app shutdown whenever possible.

Windows state is saved under `%APPDATA%\HorizonServerManager`, not tied to the browser's temporary port. No firewall changes are made. First-time LAN play may require an explicitly approved Windows firewall rule. Existing source-only Tauri browser storage is not automatically imported; existing server files are not deleted.

The desktop control host binds **only to `127.0.0.1` on an OS-assigned port**, checks Host/Origin, and requires a cryptographically random per-launch header token for every command. The token travels to the UI in a URL fragment, is removed from the address bar, and is not logged or included in activity/history. Do not publish or proxy this private control session. Minecraft's selected server bind address is a separate setting.

## Development and reproducible builds

Prerequisites: Node 22+, npm, and Go 1.25+ (CI pins Go 1.27.4). The Go core uses the standard library only.

```sh
npm ci
npm run dev                 # UI-only preview, no filesystem/process privileges
npm run build               # TypeScript + Vite, copies embedded UI assets
npm test                    # go test -race ./...
go vet ./...
npm run desktop             # build/run the Go desktop launcher
npm run build:windows       # cross-compile x64 and ARM64 portable EXEs to release/
```

For Android, install JDK 17, Gradle 8.13, SDK platform/build-tools 35, and NDK `28.0.13004108` on Linux, then:

```sh
npm ci && npm run build
export ANDROID_HOME=/path/to/android-sdk
export ANDROID_NDK_HOME="$ANDROID_HOME/ndk/28.0.13004108"
bash scripts/build-android.sh
# Optional connected-device/emulator smoke tests:
gradle -p android :app:connectedDebugAndroidTest
```

The Android build includes both ARM64 and x86-64 Go shared libraries and performs Android lint. `assembleDebug` uses a development signing key. Production signing keys must be kept in a secure external signing system, never in Git. Generated UI assets, SDKs, signing keys, and binaries are ignored.

## CI and verification

`.github/workflows/build.yml` runs Go race tests, frontend compilation, native Windows tests, Windows EXE builds, Android JNI/APK compilation, lint, and Android-emulator smoke tests. Successful runs retain downloadable artifacts; public releases copy the binaries into anonymous-download release assets with SHA-256 sums. A workflow definition alone is not evidence of a successful build: check the run linked from a release.

Tests cover network classification, port checks, no-overwrite directories, consent/checksum failures, installed identity, properties, preferences, archive attacks, running-server guards, real child-process I/O, graceful/forced stop, crashes, loopback authentication, and origin/host checks. Emulator smoke tests check JNI initialization, the Android runtime refusal, storage boundaries, and the actual packaged UI. Physical Android devices and real Minecraft worlds/Java versions still require manual testing; no Android hosting or production-readiness claim is made.

See [the migration plan](docs/go-migration.md) for architecture decisions and remaining platform work.
