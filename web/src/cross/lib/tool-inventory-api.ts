import { api } from './api';
import type { InventoryMetricRecords, InventoryToolsResponse } from '../types/tool-inventory';

export async function fetchInventoryTools(signal?: AbortSignal): Promise<InventoryToolsResponse> {
  const data = await api<InventoryToolsResponse>('/inventory/tools', { signal });
  return { generated_at: data?.generated_at ?? '', tools: Array.isArray(data?.tools) ? data.tools : [] };
}

export async function fetchInventoryMetricRecords(metricId: string, connectionId: string, signal?: AbortSignal): Promise<InventoryMetricRecords> {
  const query = new URLSearchParams({ connection: connectionId });
  return api<InventoryMetricRecords>(`/inventory/metrics/${encodeURIComponent(metricId)}/records?${query}`, { signal });
}
