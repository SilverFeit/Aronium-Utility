import { invoke } from '@tauri-apps/api/core';
import type { DebugLogsPayload } from './types';

/**
 * Aronium Utility UI — Tauri 2 IPC + Sidecar Bridge.
 *
 * Delegates 100% of business logic execution to the existing Go CLI (`aronium-util`)
 * and parses its JSON output. Zero business logic is duplicated in the UI.
 */
function isTauriRuntime(): boolean {
  return typeof window !== 'undefined' && ('__TAURI_INTERNALS__' in window || '__TAURI__' in window);
}

export async function runAroniumCLI<T>(args: string[]): Promise<T> {
  if (isTauriRuntime()) {
    const rawStdout = await invoke<string>('run_aronium_cli', { args });
    return JSON.parse(rawStdout) as T;
  }

  // Native Sidecar IPC Host fallback (when launched via `aronium-utility-ui.exe` or `aronium-util ui`)
  const resp = await fetch('/api/ipc/cli', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ args }),
  });

  if (!resp.ok) {
    const errText = await resp.text();
    throw new Error(errText || `Error ejecutando CLI (${resp.status})`);
  }

  return (await resp.json()) as T;
}

export async function fetchDebugLogs(): Promise<DebugLogsPayload> {
  if (isTauriRuntime()) {
    return await runAroniumCLI<DebugLogsPayload>(['logs', '--json']);
  }
  const resp = await fetch('/api/debug/logs');
  if (!resp.ok) {
    throw new Error((await resp.text()) || `HTTP ${resp.status}`);
  }
  return (await resp.json()) as DebugLogsPayload;
}

export async function clearDebugLogs(): Promise<void> {
  if (isTauriRuntime()) {
    await runAroniumCLI(['logs', '--clear', '--json']);
    return;
  }
  const resp = await fetch('/api/debug/logs/clear', { method: 'POST' });
  if (!resp.ok) {
    throw new Error((await resp.text()) || `HTTP ${resp.status}`);
  }
}
