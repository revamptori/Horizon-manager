use chrono::{DateTime, Utc};
use futures_util::StreamExt;
use get_if_addrs::{get_if_addrs, IfAddr};
use serde::{Serialize, Serializer};
use sha1::{Digest, Sha1};
use std::{
    collections::HashMap,
    fs,
    io::Write,
    net::{IpAddr, Ipv4Addr, SocketAddr, TcpListener, UdpSocket},
    path::{Component, Path, PathBuf},
    process::Stdio,
    sync::Arc,
    time::{Duration, Instant},
};
use tauri::{AppHandle, Emitter, Manager, State};
use thiserror::Error;
use tokio::{io::{AsyncBufReadExt, AsyncWriteExt, BufReader}, process::{Child, ChildStdin, Command}, sync::Mutex};

const MOJANG_MANIFEST: &str = "https://piston-meta.mojang.com/mc/game/version_manifest_v2.json";

#[derive(Debug, Error)]
pub enum HorizonError {
    #[error("{0}")]
    Message(String),
    #[error(transparent)]
    Io(#[from] std::io::Error),
    #[error(transparent)]
    Http(#[from] reqwest::Error),
    #[error(transparent)]
    Json(#[from] serde_json::Error),
}

impl Serialize for HorizonError {
    fn serialize<S: Serializer>(&self, serializer: S) -> std::result::Result<S::Ok, S::Error> {
        serializer.serialize_str(&self.to_string())
    }
}

type Result<T> = std::result::Result<T, HorizonError>;

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct NetworkInterface {
    pub name: String,
    pub ipv4: String,
    pub private: bool,
    pub selected: bool,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct NetworkSnapshot {
    pub interfaces: Vec<NetworkInterface>,
    pub selected_private_ipv4: Option<String>,
    pub recommended_port: u16,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct MinecraftVersion {
    pub id: String,
    pub version_type: String,
    pub release_time: String,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct InstallResult {
    pub jar_path: String,
    pub version: String,
    pub bytes_downloaded: u64,
    pub sha1_verified: bool,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ServerStatus {
    pub running: bool,
    pub pid: Option<u32>,
    pub exit_code: Option<i32>,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct BackupInfo {
    pub path: String,
    pub size_bytes: u64,
    pub created_at: DateTime<Utc>,
}

struct ManagedProcess {
    child: Child,
    stdin: ChildStdin,
}

#[derive(Default)]
pub struct AppState {
    processes: Mutex<HashMap<String, ManagedProcess>>,
}

fn is_rfc1918(ip: Ipv4Addr) -> bool {
    let [a, b, _, _] = ip.octets();
    a == 10 || (a == 172 && (16..=31).contains(&b)) || (a == 192 && b == 168)
}

fn route_ipv4() -> Option<Ipv4Addr> {
    let socket = UdpSocket::bind((Ipv4Addr::UNSPECIFIED, 0)).ok()?;
    socket.connect((Ipv4Addr::new(1, 1, 1, 1), 80)).ok()?;
    match socket.local_addr().ok()?.ip() {
        IpAddr::V4(ip) if is_rfc1918(ip) => Some(ip),
        _ => None,
    }
}

fn port_is_available(ip: Ipv4Addr, port: u16) -> bool {
    TcpListener::bind(SocketAddr::from((ip, port))).is_ok()
}

fn choose_port(ip: Ipv4Addr) -> u16 {
    (25565..=25665).find(|port| port_is_available(ip, *port)).unwrap_or(25565)
}

fn network_snapshot_impl() -> Result<NetworkSnapshot> {
    let routed = route_ipv4();
    let mut interfaces = Vec::new();
    for iface in get_if_addrs().map_err(|e| HorizonError::Message(format!("Could not inspect network interfaces: {e}")))? {
        if let IfAddr::V4(v4) = iface.addr {
            if v4.ip.is_loopback() || v4.ip.is_unspecified() { continue; }
            interfaces.push(NetworkInterface {
                name: iface.name,
                ipv4: v4.ip.to_string(),
                private: is_rfc1918(v4.ip),
                selected: routed == Some(v4.ip),
            });
        }
    }
    interfaces.sort_by(|a, b| b.selected.cmp(&a.selected).then_with(|| a.name.cmp(&b.name)));
    let selected_private_ipv4 = routed.map(|ip| ip.to_string()).or_else(|| interfaces.iter().find(|i| i.private).map(|i| i.ipv4.clone()));
    let bind_ip = selected_private_ipv4.as_deref().and_then(|v| v.parse().ok()).unwrap_or(Ipv4Addr::LOCALHOST);
    Ok(NetworkSnapshot { interfaces, selected_private_ipv4, recommended_port: choose_port(bind_ip) })
}

#[tauri::command]
fn network_snapshot() -> Result<NetworkSnapshot> { network_snapshot_impl() }

#[tauri::command]
fn check_port_available(address: String, port: u16) -> Result<bool> {
    let ip: Ipv4Addr = address.parse().map_err(|_| HorizonError::Message("Binding address must be an IPv4 address".into()))?;
    Ok(port_is_available(ip, port))
}

#[tauri::command]
fn default_server_directory(app: AppHandle, name: String) -> Result<String> {
    let slug: String = name.trim().chars().map(|c| if c.is_ascii_alphanumeric() || c == '-' || c == '_' { c } else { '-' }).collect();
    let slug = slug.trim_matches('-');
    if slug.is_empty() { return Err(HorizonError::Message("Enter a server name to choose its folder".into())); }
    let base = app.path().app_data_dir().map_err(|e| HorizonError::Message(format!("Could not locate Horizon's application data directory: {e}")))?;
    Ok(base.join("servers").join(slug).to_string_lossy().into_owned())
}

#[tauri::command]
async fn list_minecraft_versions() -> Result<Vec<MinecraftVersion>> {
    let response: serde_json::Value = reqwest::Client::builder().user_agent("Horizon-Server-Manager/0.1").connect_timeout(Duration::from_secs(15)).timeout(Duration::from_secs(30)).build()?.get(MOJANG_MANIFEST).send().await?.error_for_status()?.json().await?;
    let versions = response["versions"].as_array().ok_or_else(|| HorizonError::Message("Mojang returned an unexpected version manifest".into()))?;
    Ok(versions.iter().filter(|v| v["type"] == "release").take(30).filter_map(|v| Some(MinecraftVersion {
        id: v["id"].as_str()?.to_owned(),
        version_type: v["type"].as_str()?.to_owned(),
        release_time: v["releaseTime"].as_str()?.to_owned(),
    })).collect())
}

fn validate_binding(address: &str, port: u16) -> Result<()> {
    if port < 1024 { return Err(HorizonError::Message("Choose a port between 1024 and 65535".into())); }
    let ip: Ipv4Addr = address.parse().map_err(|_| HorizonError::Message("Binding address must be 0.0.0.0 or a private IPv4 address on this device".into()))?;
    if ip == Ipv4Addr::UNSPECIFIED {
        if !port_is_available(ip, port) { return Err(HorizonError::Message(format!("TCP port {port} is already in use on all IPv4 interfaces. Choose another port."))); }
        return Ok(());
    }
    if !is_rfc1918(ip) { return Err(HorizonError::Message("Server binding must use a detected private IPv4 address. Public and CGNAT addresses are not accepted.".into())); }
    let local = get_if_addrs().map_err(|e| HorizonError::Message(format!("Could not inspect local interfaces: {e}")))?.iter().any(|iface| matches!(&iface.addr, IfAddr::V4(v4) if v4.ip == ip));
    if !local { return Err(HorizonError::Message("That private address is not assigned to a local interface anymore. Refresh network detection and choose an active interface.".into())); }
    if !port_is_available(ip, port) { return Err(HorizonError::Message(format!("TCP port {port} is already in use on {address}. Choose another port."))); }
    Ok(())
}

fn write_server_properties(directory: &Path, address: &str, port: u16) -> Result<()> {
    validate_binding(address, port)?;
    write_server_properties_unchecked(directory, address, port)
}

fn write_server_properties_unchecked(directory: &Path, address: &str, port: u16) -> Result<()> {
    let path = directory.join("server.properties");
    let existing = if path.exists() { fs::read_to_string(&path)? } else { String::new() };
    let mut lines = Vec::new();
    let mut saw_ip = false;
    let mut saw_port = false;
    for line in existing.lines() {
        if line.starts_with("server-ip=") { lines.push(format!("server-ip={address}")); saw_ip = true; }
        else if line.starts_with("server-port=") { lines.push(format!("server-port={port}")); saw_port = true; }
        else { lines.push(line.to_string()); }
    }
    if !saw_ip { lines.push(format!("server-ip={address}")); }
    if !saw_port { lines.push(format!("server-port={port}")); }
    let temp = directory.join("server.properties.horizon-tmp");
    let backup = directory.join("server.properties.horizon-backup");
    fs::write(&temp, format!("{}\n", lines.join("\n")))?;
    if path.exists() {
        if backup.exists() { return Err(HorizonError::Message("A previous configuration update needs recovery; server.properties.horizon-backup was preserved.".into())); }
        fs::rename(&path, &backup)?;
        if let Err(error) = fs::rename(&temp, &path) {
            let _ = fs::rename(&backup, &path);
            return Err(HorizonError::Io(error));
        }
        fs::remove_file(backup)?;
    } else {
        fs::rename(temp, path)?;
    }
    Ok(())
}

#[tauri::command]
async fn set_server_binding(state: State<'_, Arc<AppState>>, server_id: String, directory: String, address: String, port: u16) -> Result<()> {
    if !safe_server_id(&server_id) { return Err(HorizonError::Message("Invalid local server identifier".into())); }
    let mut processes = state.processes.lock().await;
    let is_running = match processes.get_mut(&server_id) { Some(process) => process.child.try_wait()?.is_none(), None => false };
    if is_running { return Err(HorizonError::Message("Stop this server before changing its network binding.".into())); }
    processes.remove(&server_id);
    let root = PathBuf::from(directory);
    if !root.is_absolute() || !root.is_dir() || !root.join("server.jar").is_file() { return Err(HorizonError::Message("Install the server before changing its binding.".into())); }
    verify_installed_server(&root, &server_id)?;
    write_server_properties(&root, &address, port)
}

fn ensure_empty_or_managed_dir(dir: &Path) -> Result<()> {
    if dir.exists() {
        if !dir.is_dir() { return Err(HorizonError::Message("The chosen server path is not a directory".into())); }
        let mut entries = fs::read_dir(dir)?;
        if entries.next().is_some() {
            return Err(HorizonError::Message("The chosen directory is not empty. Pick a new folder; Horizon will never overwrite an existing world.".into()));
        }
    } else {
        fs::create_dir_all(dir)?;
    }
    Ok(())
}

#[tauri::command]
fn prepare_server_directory(directory: String) -> Result<String> {
    let path = PathBuf::from(directory.trim());
    if path.as_os_str().is_empty() { return Err(HorizonError::Message("Choose a server directory first".into())); }
    if !path.is_absolute() { return Err(HorizonError::Message("Use an absolute path for the server directory.".into())); }
    ensure_empty_or_managed_dir(&path)?;
    Ok(path.to_string_lossy().into_owned())
}

#[tauri::command]
async fn install_vanilla_server(app: AppHandle, server_id: String, version: String, directory: String, bind_address: String, port: u16, accept_eula: bool) -> Result<InstallResult> {
    if !safe_server_id(&server_id) { return Err(HorizonError::Message("Invalid local server identifier".into())); }
    if !accept_eula { return Err(HorizonError::Message("You must explicitly accept the Minecraft EULA before installing a server.".into())); }
    let version = version.trim();
    if version.is_empty() || version.len() > 32 || !version.chars().all(|c| c.is_ascii_alphanumeric() || c == '.' || c == '-') {
        return Err(HorizonError::Message("Invalid Minecraft version identifier".into()));
    }
    let dir = PathBuf::from(directory);
    if !dir.is_absolute() { return Err(HorizonError::Message("Use an absolute server directory path.".into())); }
    if !dir.is_dir() { return Err(HorizonError::Message("The server directory has not been prepared".into())); }
    if fs::read_dir(&dir)?.next().is_some() { return Err(HorizonError::Message("The server directory changed after it was prepared. Installation requires an empty folder; existing files were left untouched.".into())); }
    validate_binding(&bind_address, port)?;
    let jar_path = dir.join("server.jar");
    if jar_path.exists() { return Err(HorizonError::Message("server.jar already exists. Horizon will not replace installed server files.".into())); }

    let client = reqwest::Client::builder().user_agent("Horizon-Server-Manager/0.1").connect_timeout(Duration::from_secs(15)).timeout(Duration::from_secs(600)).build()?;
    let manifest: serde_json::Value = client.get(MOJANG_MANIFEST).send().await?.error_for_status()?.json().await?;
    let manifest_entry = manifest["versions"].as_array().and_then(|v| v.iter().find(|entry| entry["id"] == version)).ok_or_else(|| HorizonError::Message(format!("Minecraft release {version} was not found in Mojang's official release manifest".into())))?;
    let meta_url = manifest_entry["url"].as_str().ok_or_else(|| HorizonError::Message("Mojang manifest entry did not include a version metadata URL".into()))?;
    let metadata: serde_json::Value = client.get(meta_url).send().await?.error_for_status()?.json().await?;
    let server = &metadata["downloads"]["server"];
    let url = server["url"].as_str().ok_or_else(|| HorizonError::Message(format!("Mojang does not publish a downloadable server jar for Minecraft {version}")))?;
    let expected_sha1 = server["sha1"].as_str().ok_or_else(|| HorizonError::Message("Mojang metadata did not include a SHA-1 checksum; refusing an unverifiable download".into()))?;
    let response = client.get(url).send().await?.error_for_status()?;
    let expected_size = response.content_length();
    let mut stream = response.bytes_stream();
    let mut bytes = Vec::with_capacity(expected_size.unwrap_or(0).min(200_000_000) as usize);
    let mut downloaded = 0_u64;
    let transfer_started = Instant::now();
    while let Some(chunk) = stream.next().await {
        let chunk = chunk?;
        downloaded += chunk.len() as u64;
        if downloaded > 200_000_000 { return Err(HorizonError::Message("Server download exceeded the 200 MB safety limit.".into())); }
        bytes.extend_from_slice(&chunk);
        let bytes_per_second = (downloaded as f64 / transfer_started.elapsed().as_secs_f64().max(0.001)) as u64;
        let _ = app.emit("installation-progress", serde_json::json!({"operation":"Downloading Vanilla server", "downloadedBytes":downloaded, "totalBytes":expected_size, "bytesPerSecond":bytes_per_second}));
    }
    let actual_sha1 = sha1_hex(&bytes);
    if !actual_sha1.eq_ignore_ascii_case(expected_sha1) {
        return Err(HorizonError::Message("Downloaded server jar failed Mojang's SHA-1 verification; the file was discarded.".into()));
    }
    let temp = dir.join("server.jar.download");
    fs::write(&temp, &bytes)?;
    fs::rename(&temp, &jar_path)?;
    fs::write(dir.join("eula.txt"), "# Accepted in Horizon Server Manager after user confirmation\neula=true\n")?;
    write_server_properties_unchecked(&dir, &bind_address, port)?;
    fs::write(dir.join(".horizon-server.json"), serde_json::to_vec(&serde_json::json!({ "serverId": server_id, "software": "Vanilla", "version": version, "sha1": actual_sha1 }))?)?;
    Ok(InstallResult { jar_path: jar_path.to_string_lossy().into_owned(), version: version.into(), bytes_downloaded: bytes.len() as u64, sha1_verified: true })
}

fn safe_server_id(id: &str) -> bool {
    !id.is_empty() && id.len() <= 80 && id.chars().all(|c| c.is_ascii_alphanumeric() || c == '-' || c == '_')
}

fn sha1_hex(bytes: &[u8]) -> String { hex::encode(Sha1::digest(bytes)) }

fn verify_installed_server(directory: &Path, server_id: &str) -> Result<()> {
    let marker: serde_json::Value = serde_json::from_slice(&fs::read(directory.join(".horizon-server.json")).map_err(|_| HorizonError::Message("This directory is not a verified Horizon installation. Install server software through Horizon first.".into()))?)?;
    if marker["serverId"].as_str() != Some(server_id) || marker["software"].as_str() != Some("Vanilla") {
        return Err(HorizonError::Message("Server identity does not match the verified installation record.".into()));
    }
    let expected = marker["sha1"].as_str().ok_or_else(|| HorizonError::Message("Verified installation record is incomplete".into()))?;
    let actual = sha1_hex(&fs::read(directory.join("server.jar"))?);
    if !actual.eq_ignore_ascii_case(expected) { return Err(HorizonError::Message("server.jar no longer matches the verified Mojang download. Refusing to launch an unverified file.".into())); }
    Ok(())
}

#[tauri::command]
async fn start_server(app: AppHandle, state: State<'_, Arc<AppState>>, server_id: String, directory: String, memory_mb: u32, java_path: Option<String>) -> Result<ServerStatus> {
    if !safe_server_id(&server_id) { return Err(HorizonError::Message("Invalid local server identifier".into())); }
    if !(512..=32768).contains(&memory_mb) { return Err(HorizonError::Message("Memory allocation must be between 512 MB and 32768 MB".into())); }
    let mut processes = state.processes.lock().await;
    let already_running = processes.get_mut(&server_id).map(|process| process.child.try_wait()).transpose()?.is_some_and(|status| status.is_none());
    if already_running { return Err(HorizonError::Message("This server is already running".into())); }
    processes.remove(&server_id);
    let dir = PathBuf::from(directory);
    if !dir.is_absolute() { return Err(HorizonError::Message("Server directory path must be absolute.".into())); }
    if !dir.is_dir() || !dir.join("server.jar").is_file() { return Err(HorizonError::Message("Install the server software before starting it.".into())); }
    verify_installed_server(&dir, &server_id)?;
    let properties = fs::read_to_string(dir.join("server.properties"))?;
    let bind_address = properties.lines().find_map(|line| line.strip_prefix("server-ip=")).unwrap_or("");
    let bind_address = if bind_address.is_empty() { "0.0.0.0" } else { bind_address };
    let port = properties.lines().find_map(|line| line.strip_prefix("server-port=")).and_then(|p| p.parse::<u16>().ok()).unwrap_or(0);
    validate_binding(bind_address, port)?;
    let eula = fs::read_to_string(dir.join("eula.txt")).map_err(|_| HorizonError::Message("The EULA has not been accepted. Review and accept it in the setup flow before starting.".into()))?;
    if !eula.lines().any(|l| l.trim() == "eula=true") { return Err(HorizonError::Message("The EULA has not been accepted. Review and accept it in the setup flow before starting.".into())); }
    let executable = java_path.filter(|p| !p.trim().is_empty()).unwrap_or_else(|| "java".to_owned());
    let mut child = Command::new(executable).arg(format!("-Xms{}M", memory_mb.min(1024))).arg(format!("-Xmx{memory_mb}M"))
        .arg("-jar").arg("server.jar").arg("nogui").current_dir(&dir)
        .stdin(Stdio::piped()).stdout(Stdio::piped()).stderr(Stdio::piped()).kill_on_drop(true).spawn()
        .map_err(|e| HorizonError::Message(format!("Could not launch Java. Check the installed runtime and Java path: {e}")))?;
    let pid = child.id();
    let stdin = child.stdin.take().ok_or_else(|| HorizonError::Message("Could not open the server command pipe".into()))?;
    let stdout = child.stdout.take();
    let stderr = child.stderr.take();
    if let Some(out) = stdout { spawn_output_reader(app.clone(), server_id.clone(), out, "stdout"); }
    if let Some(err) = stderr { spawn_output_reader(app, server_id.clone(), err, "stderr"); }
    processes.insert(server_id, ManagedProcess { child, stdin });
    Ok(ServerStatus { running: true, pid, exit_code: None })
}

fn spawn_output_reader<R>(app: AppHandle, server_id: String, reader: R, stream: &'static str)
where R: tokio::io::AsyncRead + Unpin + Send + 'static {
    tauri::async_runtime::spawn(async move {
        let mut lines = BufReader::new(reader).lines();
        while let Ok(Some(line)) = lines.next_line().await {
            let _ = app.emit("server-output", serde_json::json!({"serverId": server_id, "stream": stream, "timestamp": Utc::now().to_rfc3339(), "line": line}));
        }
    });
}

#[tauri::command]
async fn server_status(state: State<'_, Arc<AppState>>, server_id: String) -> Result<ServerStatus> {
    let mut processes = state.processes.lock().await;
    let Some(exit_status) = processes.get_mut(&server_id).map(|process| process.child.try_wait()).transpose()? else {
        return Ok(ServerStatus { running: false, pid: None, exit_code: None });
    };
    if let Some(status) = exit_status {
        processes.remove(&server_id);
        Ok(ServerStatus { running: false, pid: None, exit_code: status.code() })
    } else {
        let pid = processes.get(&server_id).and_then(|process| process.child.id());
        Ok(ServerStatus { running: true, pid, exit_code: None })
    }
}

#[tauri::command]
async fn send_server_command(state: State<'_, Arc<AppState>>, server_id: String, command: String) -> Result<()> {
    if command.len() > 2048 || command.contains('\n') || command.contains('\r') || command.contains('\0') {
        return Err(HorizonError::Message("Enter a single server command under 2048 characters".into()));
    }
    let mut processes = state.processes.lock().await;
    let exited = processes.get_mut(&server_id).ok_or_else(|| HorizonError::Message("The server is stopped; commands are not sent to stopped servers.".into()))?.child.try_wait()?.is_some();
    if exited { processes.remove(&server_id); return Err(HorizonError::Message("The server has exited; the command was not sent.".into())); }
    let process = processes.get_mut(&server_id).ok_or_else(|| HorizonError::Message("The server is stopped; commands are not sent to stopped servers.".into()))?;
    process.stdin.write_all(command.trim().as_bytes()).await?;
    process.stdin.write_all(b"\n").await?;
    process.stdin.flush().await?;
    Ok(())
}

#[tauri::command]
async fn stop_server(state: State<'_, Arc<AppState>>, server_id: String) -> Result<ServerStatus> {
    let mut processes = state.processes.lock().await;
    let Some(mut process) = processes.remove(&server_id) else { return Ok(ServerStatus { running: false, pid: None, exit_code: None }); };
    if process.child.try_wait()?.is_some() { return Ok(ServerStatus { running: false, pid: None, exit_code: None }); }
    process.stdin.write_all(b"stop\n").await?;
    process.stdin.flush().await?;
    drop(process.stdin);
    let status = tokio::time::timeout(Duration::from_secs(20), process.child.wait()).await;
    let exit_code = match status {
        Ok(result) => result?.code(),
        Err(_) => { process.child.kill().await?; process.child.wait().await?.code() }
    };
    Ok(ServerStatus { running: false, pid: None, exit_code })
}

fn ignored_backup_path(relative: &Path) -> bool {
    relative.components().any(|c| matches!(c, Component::Normal(n) if matches!(n.to_str(), Some("logs" | "crash-reports" | "cache" | ".horizon-backups"))))
        || relative.file_name().and_then(|n| n.to_str()).is_some_and(|n| n.ends_with(".tmp") || n.ends_with(".download") || n.ends_with(".horizon-tmp") || n.ends_with(".horizon-backup"))
}

fn add_directory_to_zip<W: Write + std::io::Seek>(zip: &mut zip::ZipWriter<W>, root: &Path, dir: &Path) -> Result<()> {
    for entry in fs::read_dir(dir)? {
        let entry = entry?;
        let path = entry.path();
        let relative = path.strip_prefix(root).map_err(|_| HorizonError::Message("Backup path escaped its server directory".into()))?;
        if ignored_backup_path(relative) { continue; }
        if entry.file_type()?.is_symlink() { continue; }
        if entry.file_type()?.is_dir() {
            add_directory_to_zip(zip, root, &path)?;
        } else if entry.file_type()?.is_file() {
            let options = zip::write::SimpleFileOptions::default().compression_method(zip::CompressionMethod::Deflated);
            zip.start_file(relative.to_string_lossy().replace('\\', "/"), options).map_err(|e| HorizonError::Message(format!("Could not add file to backup: {e}")))?;
            let mut file = fs::File::open(&path)?;
            std::io::copy(&mut file, zip).map_err(HorizonError::Io)?;
        }
    }
    Ok(())
}

#[tauri::command]
async fn create_backup(state: State<'_, Arc<AppState>>, server_id: String, directory: String) -> Result<BackupInfo> {
    if !safe_server_id(&server_id) { return Err(HorizonError::Message("Invalid local server identifier".into())); }
    let mut processes = state.processes.lock().await;
    let is_running = match processes.get_mut(&server_id) {
        Some(process) => process.child.try_wait()?.is_none(),
        None => false,
    };
    if is_running { return Err(HorizonError::Message("Stop the server before creating a manual backup. A live-world archive may be inconsistent.".into())); }
    processes.remove(&server_id);
    let root = PathBuf::from(directory);
    if !root.is_absolute() || !root.is_dir() { return Err(HorizonError::Message("Server directory does not exist or is not an absolute path".into())); }
    verify_installed_server(&root, &server_id)?;
    let backup_dir = root.join(".horizon-backups");
    fs::create_dir_all(&backup_dir)?;
    let timestamp = Utc::now();
    let path = backup_dir.join(format!("backup-{}.zip", timestamp.format("%Y%m%d-%H%M%S-%f")));
    let backup_result = (|| -> Result<()> {
        let file = fs::OpenOptions::new().write(true).create_new(true).open(&path)?;
        let mut zip = zip::ZipWriter::new(file);
        add_directory_to_zip(&mut zip, &root, &root)?;
        let file = zip.finish().map_err(|e| HorizonError::Message(format!("Could not finish backup archive: {e}")))?;
        file.sync_all()?;
        Ok(())
    })();
    if let Err(error) = backup_result { let _ = fs::remove_file(&path); return Err(error); }
    let size_bytes = fs::metadata(&path)?.len();
    Ok(BackupInfo { path: path.to_string_lossy().into_owned(), size_bytes, created_at: timestamp })
}

#[tauri::command]
fn restore_backup(server_id: String, server_directory: String, backup_path: String, destination: String, confirmed: bool) -> Result<String> {
    if !confirmed { return Err(HorizonError::Message("Confirm the restore operation before continuing.".into())); }
    if !safe_server_id(&server_id) { return Err(HorizonError::Message("Invalid local server identifier".into())); }
    let root = PathBuf::from(server_directory);
    if !root.is_absolute() || !root.is_dir() { return Err(HorizonError::Message("Server directory does not exist".into())); }
    verify_installed_server(&root, &server_id)?;
    let archive = PathBuf::from(backup_path);
    if !archive.is_file() { return Err(HorizonError::Message("Backup archive was not found".into())); }
    let backup_parent = root.join(".horizon-backups").canonicalize()?;
    if archive.canonicalize()?.parent() != Some(backup_parent.as_path()) { return Err(HorizonError::Message("The selected archive is not inside this server's Horizon backup folder.".into())); }
    let destination = PathBuf::from(destination);
    if !destination.is_absolute() { return Err(HorizonError::Message("Restore destination must be an absolute path.".into())); }
    let canonical_root = root.canonicalize()?;
    let root_parent = canonical_root.parent().ok_or_else(|| HorizonError::Message("Could not determine server directory parent".into()))?;
    let destination_parent = destination.parent().ok_or_else(|| HorizonError::Message("Restore destination must be a sibling of the server folder.".into()))?.canonicalize()?;
    let root_name = canonical_root.file_name().and_then(|name| name.to_str()).ok_or_else(|| HorizonError::Message("Could not determine server folder name".into()))?;
    let restored_prefix = format!("{root_name}-restored-");
    let destination_name = destination.file_name().and_then(|name| name.to_str()).ok_or_else(|| HorizonError::Message("Could not determine restore folder name".into()))?;
    if destination_parent.as_path() != root_parent || !destination_name.starts_with(&restored_prefix) {
        return Err(HorizonError::Message("Restore destination must be a new, clearly named sibling folder. Existing server files will not be replaced.".into()));
    }
    ensure_empty_or_managed_dir(&destination)?;
    let file = fs::File::open(archive)?;
    let mut zip = zip::ZipArchive::new(file).map_err(|e| HorizonError::Message(format!("Backup archive is invalid: {e}")))?;
    if zip.len() > 100_000 { return Err(HorizonError::Message("Backup contains too many entries; restore was stopped for safety.".into())); }
    let mut expanded_bytes = 0_u64;
    for index in 0..zip.len() {
        let mut entry = zip.by_index(index).map_err(|e| HorizonError::Message(format!("Could not read backup entry: {e}")))?;
        expanded_bytes = expanded_bytes.checked_add(entry.size()).ok_or_else(|| HorizonError::Message("Backup expanded size overflowed the safety limit".into()))?;
        if expanded_bytes > 50 * 1024 * 1024 * 1024 { return Err(HorizonError::Message("Backup expands beyond the 50 GB restore safety limit.".into())); }
        let Some(enclosed) = entry.enclosed_name().map(|p| p.to_path_buf()) else { return Err(HorizonError::Message("Backup contains an unsafe path and was rejected".into())); };
        if enclosed.components().any(|c| matches!(c, Component::ParentDir | Component::RootDir | Component::Prefix(_))) {
            return Err(HorizonError::Message("Backup contains a path traversal entry and was rejected".into()));
        }
        let out = destination.join(enclosed);
        if entry.is_dir() { fs::create_dir_all(&out)?; continue; }
        if out.exists() { return Err(HorizonError::Message("Backup contains duplicate file paths; restore was stopped rather than overwrite extracted data.".into())); }
        if let Some(parent) = out.parent() { fs::create_dir_all(parent)?; }
        let mut output = fs::File::create(out)?;
        std::io::copy(&mut entry, &mut output)?;
    }
    Ok(destination.to_string_lossy().into_owned())
}

pub fn run() {
    let state = Arc::new(AppState::default());
    tauri::Builder::default()
        .manage(state)
        .invoke_handler(tauri::generate_handler![network_snapshot, check_port_available, default_server_directory, list_minecraft_versions, prepare_server_directory, set_server_binding, install_vanilla_server, start_server, server_status, send_server_command, stop_server, create_backup, restore_backup])
        .run(tauri::generate_context!())
        .expect("error while running Horizon Server Manager");
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn classifies_only_rfc1918_networks_as_private() {
        for ip in ["10.0.0.1", "172.16.0.1", "172.31.255.254", "192.168.1.5"] { assert!(is_rfc1918(ip.parse().unwrap()), "{ip}"); }
        for ip in ["172.15.0.1", "172.32.0.1", "100.64.1.1", "8.8.8.8", "127.0.0.1"] { assert!(!is_rfc1918(ip.parse().unwrap()), "{ip}"); }
    }

    #[test]
    fn recommends_an_available_host_port() {
        let port = choose_port(Ipv4Addr::LOCALHOST);
        assert!((25565..=25665).contains(&port));
        let reservation = TcpListener::bind((Ipv4Addr::LOCALHOST, port)).unwrap();
        assert!(!port_is_available(Ipv4Addr::LOCALHOST, port));
        drop(reservation);
    }

    #[test]
    fn refuses_non_empty_server_directory() {
        let dir = tempfile::tempdir().unwrap();
        fs::write(dir.path().join("world.dat"), b"keep").unwrap();
        assert!(ensure_empty_or_managed_dir(dir.path()).is_err());
        assert_eq!(fs::read(dir.path().join("world.dat")).unwrap(), b"keep");
    }

    #[test]
    fn backup_excludes_transient_files_and_restores_to_new_directory() {
        let source = tempfile::tempdir().unwrap();
        fs::create_dir(source.path().join("world" )).unwrap();
        fs::write(source.path().join("world/level.dat"), b"level").unwrap();
        fs::create_dir(source.path().join("logs")).unwrap();
        fs::write(source.path().join("logs/latest.log"), b"log").unwrap();
        let backup_dir = tempfile::tempdir().unwrap();
        let archive = backup_dir.path().join("test.zip");
        let mut zip = zip::ZipWriter::new(fs::File::create(&archive).unwrap());
        add_directory_to_zip(&mut zip, source.path(), source.path()).unwrap();
        zip.finish().unwrap();
        let mut zip = zip::ZipArchive::new(fs::File::open(archive).unwrap()).unwrap();
        assert!(zip.by_name("world/level.dat").is_ok());
        assert!(zip.by_name("logs/latest.log").is_err());
    }

    #[test]
    fn verifies_the_expected_sha1_digest() {
        assert_eq!(sha1_hex(b"abc"), "a9993e364706816aba3e25717850c26c9cd0d89d");
    }

    #[test]
    fn installed_server_marker_binds_identity_and_jar_checksum() {
        let dir = tempfile::tempdir().unwrap();
        let jar = b"verified server bytes";
        fs::write(dir.path().join("server.jar"), jar).unwrap();
        fs::write(dir.path().join(".horizon-server.json"), serde_json::json!({ "serverId": "srv1", "software": "Vanilla", "sha1": sha1_hex(jar) }).to_string()).unwrap();
        assert!(verify_installed_server(dir.path(), "srv1").is_ok());
        assert!(verify_installed_server(dir.path(), "other").is_err());
        fs::write(dir.path().join("server.jar"), b"changed bytes").unwrap();
        assert!(verify_installed_server(dir.path(), "srv1").is_err());
    }

    #[test]
    fn rejects_zip_path_traversal() {
        let root = tempfile::tempdir().unwrap();
        let server = root.path().join("server");
        fs::create_dir(&server).unwrap();
        let jar = b"verified server bytes";
        fs::write(server.join("server.jar"), jar).unwrap();
        fs::write(server.join(".horizon-server.json"), serde_json::json!({ "serverId": "srv1", "software": "Vanilla", "sha1": sha1_hex(jar) }).to_string()).unwrap();
        let backup_dir = server.join(".horizon-backups");
        fs::create_dir(&backup_dir).unwrap();
        let archive = backup_dir.join("unsafe.zip");
        let mut zip = zip::ZipWriter::new(fs::File::create(&archive).unwrap());
        zip.start_file("../outside.txt", zip::write::SimpleFileOptions::default()).unwrap();
        zip.write_all(b"escape").unwrap();
        zip.finish().unwrap();
        let destination = root.path().join("server-restored-test");
        assert!(restore_backup("srv1".into(), server.to_string_lossy().to_string(), archive.to_string_lossy().to_string(), destination.to_string_lossy().to_string(), true).is_err());
        assert!(!root.path().join("outside.txt").exists());
    }

    #[test]
    fn rejects_arbitrary_ids() {
        assert!(safe_server_id("main_1"));
        assert!(!safe_server_id("../../etc"));
        assert!(!safe_server_id(""));
    }
}
