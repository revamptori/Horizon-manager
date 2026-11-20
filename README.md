# Horizon Server Manager

A local-first Minecraft server manager, built with a React/TypeScript interface and a Rust/Tauri desktop core. Android and Windows platform integrations are planned as separate platform layers; this repository currently starts with the cross-platform desktop foundation.

## Implementation plan

1. **Project structure and design system** — establish a Tauri/Rust + React/Vite workspace, accessible navigation, restrained dark/light themes, and reusable layout primitives.
2. **Cross-platform application shell** — make the app responsive and add a server-creation workflow with persisted local profiles.
3. **Server lifecycle** — Rust-managed server directories, verified Vanilla downloads, explicit EULA acceptance, process start/stop, and streamed output. Expand to other server distributions after the engine contracts are stable.
4. **Networking** — inspect host interfaces, RFC 1918 addresses, and available ports; default to a selected private interface and expose all-interface binding as an explicit setting.
5. **Console** — connect live process output to a terminal-style UI and support safe command input only while running.
6. **Plugins/mods, backups, tunnel management, Windows/Android integration, and Launcher API** — build as separate capabilities with compatibility and security boundaries; do not represent unavailable integrations as operational.
7. **Testing and polish** — exercise core logic and document platform verification requirements.

## Current milestone and honest scope

The initial implementation focuses on the application shell, local server profiles, Vanilla server installation/lifecycle, host networking inspection, and local backups. Other distributions, package catalogs, public tunnel architectures, Android foreground-service integration, Windows firewall/tray integration, and the Horizon Launcher IPC are not yet implemented. Their screens must communicate this rather than simulate a successful operation.

## Development

- `npm install`
- `npm run dev` for the UI preview
- `npm run build` for the TypeScript/Vite production build
- `npm run tauri dev` for the native desktop shell (requires the Tauri v2 system prerequisites)
- `cargo test --manifest-path src-tauri/Cargo.toml` for Rust core tests

The web-only preview can edit and persist server profiles, but process, filesystem and network operations require the native Tauri host. No cloud service or remote control endpoint is exposed.
