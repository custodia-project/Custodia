/**
 * Production source registry: PAM plus the cross-shell workspaces that are
 * part of the MVP. Demo-only tools (Netskope SSE, SIA/Risk, Personas, DSPM)
 * are not imported here; Vite also stubs their lazy modules so no production
 * chunk is emitted.
 */
import type { Domain, DomainRegistration, FrontendRegistration, ManualSyncAction, RegisteredWorkspace, RuntimeRegistration, SyncRegistration } from './source-contract.ts';
import type { WidgetCommandSources } from './cross/lib/command-center.ts';
import { registration as cyberark_pam } from './cyberark/pam/source.ts';
import { registration as cyberark_core } from './cyberark/core-source.ts';
import { coreRegistration as cross } from './cross/source.ts';

export const FRONTEND_SOURCES = [cyberark_pam, cyberark_core, cross] as const satisfies readonly FrontendRegistration[];
const registrations: readonly FrontendRegistration[] = FRONTEND_SOURCES;
export type RegisteredWorkspaceId = (typeof FRONTEND_SOURCES)[number]['workspaces'][number]['id'];
export type RegisteredCategoryId = (typeof FRONTEND_SOURCES)[number]['categories'][number]['id'];
export const REGISTERED_WORKSPACES = registrations.flatMap((source) => source.workspaces).sort((left, right) => left.order - right.order) as readonly (RegisteredWorkspace & { id: RegisteredWorkspaceId })[];
export const REGISTERED_CATEGORIES = registrations.flatMap((source) => source.categories).sort((left, right) => left.order - right.order);
export const REGISTERED_DOMAINS = registrations.flatMap((source): DomainRegistration[] => [...source.domains]).sort((left, right) => left.order - right.order);
export const WORKSPACE_TOOL_LABELS = Object.fromEntries(registrations.flatMap((source) => source.workspaces.map((entry) => [entry.id, source.toolLabel ?? REGISTERED_DOMAINS.find((domain) => domain.id === entry.domain)?.label ?? entry.label]))) as Readonly<Record<RegisteredWorkspaceId, string>>;
export const REGISTERED_SYNCS = registrations.flatMap((source): SyncRegistration[] => source.sync ? [source.sync] : []);
export const SYNC_BY_SOURCE: Readonly<Record<string, SyncRegistration>> = Object.fromEntries(REGISTERED_SYNCS.map((sync) => [sync.source, sync]));
export const REGISTERED_RUNTIMES = registrations.flatMap((source): RuntimeRegistration[] => source.runtime ? [source.runtime] : []);
export const RUNTIME_BY_ID: Readonly<Record<string, RuntimeRegistration>> = Object.fromEntries(REGISTERED_RUNTIMES.map((runtime) => [runtime.id, runtime]));
export function runtimeForWorkspace(workspace: RegisteredWorkspaceId): RuntimeRegistration {
  const entry = REGISTERED_WORKSPACES.find((entry) => entry.id === workspace);
  const runtime = entry && RUNTIME_BY_ID[entry.runtime];
  if (!runtime) throw new Error(`Workspace sin runtime registrado: ${workspace}`);
  return runtime;
}
export function syncForWorkspace(workspace: RegisteredWorkspaceId): SyncRegistration | undefined {
  return registrations.find((source) => source.workspaces.some((entry) => entry.id === workspace))?.sync;
}
export async function loadWidgetCommandSources(): Promise<WidgetCommandSources> {
  const { DASHBOARD_SHORTCUTS } = await import('./cyberark/pam/lib/dashboard-metrics.ts');
  return { cyberarkWidgets: DASHBOARD_SHORTCUTS, netskopeDataWidgets: [], netskopeChartWidgets: [] };
}
const MANUAL_SYNC_ACTIONS = registrations.flatMap((source): ManualSyncAction[] => source.manualSync ? [source.manualSync] : []);
export function manualSyncForArea(domain: Domain): ManualSyncAction {
  const action = MANUAL_SYNC_ACTIONS.find((action) => action.domain === domain) ?? MANUAL_SYNC_ACTIONS.find((action) => action.domain === 'admin');
  if (!action) throw new Error('Falta la acción de sincronización transversal');
  return action;
}
