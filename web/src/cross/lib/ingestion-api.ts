import { api } from './api';
import type { ConnectorDescriptor, ConnectionGroup, ConnectionProbe, IngestionRun, SyncStage, SyncStageHistoryRow, SyncTaskSchedule, TenantConnection } from '../types/ingestion';
import type { ConnectionRequest } from './connection-onboarding';

export const INGESTION_RUNS_QUERY_KEY = ['ingestion', 'runs', 50] as const;

export async function fetchIngestionRuns(limit = 12, signal?: AbortSignal): Promise<IngestionRun[]> {
  const data = await api<{ rows: IngestionRun[] }>(`/ingestion/runs?limit=${limit}`, { signal });
  return data.rows;
}

export async function fetchIngestionRun(runId: string): Promise<IngestionRun> {
  return api<IngestionRun>(`/ingestion/runs/${encodeURIComponent(runId)}`);
}

export async function fetchSyncStages(runId: string, signal?: AbortSignal): Promise<SyncStage[]> {
  const data = await api<{ rows: SyncStage[] }>(`/ingestion/runs/${encodeURIComponent(runId)}/stages`, { signal });
  return Array.isArray(data?.rows) ? data.rows : [];
}

export async function fetchSyncStageHistory(source: string, signal?: AbortSignal): Promise<SyncStageHistoryRow[]> {
  const data = await api<{ rows: SyncStageHistoryRow[] }>(`/ingestion/sources/${encodeURIComponent(source)}/stages/history`, { signal });
  return Array.isArray(data?.rows) ? data.rows : [];
}

export async function fetchSyncTaskSchedules(signal?: AbortSignal): Promise<SyncTaskSchedule[]> {
  const data = await api<{ rows: SyncTaskSchedule[] }>('/ingestion/schedules', { signal });
  return Array.isArray(data?.rows) ? data.rows : [];
}

export async function updateSyncTaskSchedule(taskKey: string, intervalMinutes: number | null): Promise<void> {
  await api(`/ingestion/schedules/${encodeURIComponent(taskKey)}`, {
    method: 'PUT', body: JSON.stringify({ interval_minutes: intervalMinutes }),
  });
}

export async function runSyncTask(taskKey: string): Promise<{ run_id: string; status: string }> {
  return api(`/ingestion/schedules/${encodeURIComponent(taskKey)}/runs`, { method: 'POST', body: '{}' });
}

export async function fetchConnectors(signal?: AbortSignal): Promise<ConnectorDescriptor[]> {
  const data = await api<{ rows: ConnectorDescriptor[] }>('/ingestion/connectors', { signal });
  return Array.isArray(data?.rows) ? data.rows : [];
}

export async function createConnection(request: ConnectionRequest): Promise<TenantConnection> {
  return api<TenantConnection>('/ingestion/connections', { method: 'POST', body: JSON.stringify(request) });
}

export async function probeConnection(connectionId: string): Promise<ConnectionProbe> {
  return api<ConnectionProbe>(`/ingestion/connections/${encodeURIComponent(connectionId)}/probe`, { method: 'POST', body: '{}' });
}

export async function fetchConnectionGroups(signal?: AbortSignal): Promise<ConnectionGroup[]> {
  const data = await api<{ rows: ConnectionGroup[] }>('/ingestion/connections', { signal });
  return Array.isArray(data?.rows) ? data.rows : [];
}

export async function updateConnectionGroup(groupId: string, patch: { base_url?: string; auth_mode?: string; ca_file?: string; secret?: string; interval_minutes?: number; page_size?: number; max_pages?: number; max_records?: number }): Promise<void> {
  await api(`/ingestion/connections/${encodeURIComponent(groupId)}`, { method: 'PUT', body: JSON.stringify(patch) });
}

export async function rotateConnectionSecret(groupId: string, secret: string): Promise<{ version: number }> {
  return api(`/ingestion/connections/${encodeURIComponent(groupId)}/secret`, { method: 'POST', body: JSON.stringify({ secret }) });
}

export async function setConnectionState(groupId: string, action: 'pause' | 'resume' | 'retire'): Promise<void> {
  await api(`/ingestion/connections/${encodeURIComponent(groupId)}/${action}`, { method: 'POST', body: '{}' });
}
