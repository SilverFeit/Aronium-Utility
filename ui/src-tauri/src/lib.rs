use std::env;
use std::path::PathBuf;
use std::process::Command;

fn locate_aronium_cli_sidecar() -> PathBuf {
    if let Ok(custom) = env::var("ARONIUM_CLI_BIN") {
        let p = PathBuf::from(custom);
        if p.exists() {
            return p;
        }
    }

    let bin_name = if cfg!(target_os = "windows") {
        "aronium-util.exe"
    } else {
        "aronium-util"
    };

    if let Ok(exe) = env::current_exe() {
        if let Some(dir) = exe.parent() {
            let sibling = dir.join(bin_name);
            if sibling.exists() {
                return sibling;
            }
        }
    }

    if let Ok(local_app_data) = env::var("LOCALAPPDATA") {
        let installed = PathBuf::from(local_app_data)
            .join("Programs")
            .join("AroniumUtility")
            .join(bin_name);
        if installed.exists() {
            return installed;
        }
    }

    PathBuf::from(bin_name)
}

/// Tauri 2 IPC Command that invokes the existing Go CLI (`aronium-util`) as a sidecar process
/// and returns its JSON output without duplicating any business logic.
#[tauri::command]
pub fn run_aronium_cli(args: Vec<String>) -> Result<String, String> {
    let bin = locate_aronium_cli_sidecar();
    let output = Command::new(&bin)
        .args(&args)
        .output()
        .map_err(|e| format!("Failed to spawn sidecar {:?}: {}", bin, e))?;

    if !output.status.success() {
        let stderr = String::from_utf8_lossy(&output.stderr).trim().to_string();
        let stdout = String::from_utf8_lossy(&output.stdout).trim().to_string();
        return Err(if !stderr.is_empty() { stderr } else { stdout });
    }

    Ok(String::from_utf8_lossy(&output.stdout).to_string())
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .invoke_handler(tauri::generate_handler![run_aronium_cli])
        .run(tauri::generate_context!())
        .expect("error while running Aronium Utility UI Tauri application");
}
