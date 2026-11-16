export type IngestionRun = {
  run_id: string;
  source_system?: 'cyberark' | 'identity_administration' | 'sia' | 'access' | 'netskope';
  source_file?: string | null;
  task_key?: string | null;
  status: string;
  enqueued_at?: string;
  started_at: string | null;
  completed_at: string | null;
  status_message?: string | null;
  heartbeat_at?: string | null;
  lease_expires_at?: string | null;
  attempt_count?: number;
  expected_safe_count: number | null;
  received_safe_count: number;
  received_member_count: number;
  received_object_count?: number;
  received_relationship_count?: number;
  retired_object_count?: number;
  retired_relationship_count?: number;
  error_count: number;
  error_details?: Array<{ safeName?: string; error?: string }>;
  triggered_by?: string | null;
  safes_created?: number;
  safes_updated?: number;
  safes_retired?: number;
  members_created?: number;
  members_updated?: number;
  members_retired?: number;
  pam_synced?: boolean;
};

export type SyncStage = {
  stage: string;
  required: boolean;
  status: 'completed' | 'failed' | 'skipped';
  item_count: number | null;
  duration_ms: number;
  error_kind: string | null;
  detail: string | null;
};

export type SyncTaskSchedule = {
  task_key: string;
  source_system: string;
  source_file: string;
  configured: boolean;
  active: boolean;
  interval_minutes: number;
  interval_source: 'administrative' | 'environment_task' | 'environment_source' | 'task_policy' | 'global_default';
  admin_override: number | null;
  last_success_at: string | null;
  last_attempt_at: string | null;
  last_status: string | null;
  consecutive_failures: number;
  freshness: 'fresh' | 'stale' | 'unknown';
  intervals_since_success: number | null;
  stale_after_intervals: number;
  next_scheduled_at: string | null;
};

export type SyncStageHistoryRow = {
  run_id: string;
  completed_at: string;
  stage: string;
  status: 'completed' | 'failed';
  item_count: number | null;
  duration_ms: number;
};

export type ConnectorDescriptor = {
  id: string;
  name: string;
  vendor: string;
  category: string;
  status: 'available' | 'laboratory' | 'planned';
  distribution?: 'open-source' | 'commercial';
  auth_modes: string[];
  collections: string[];
  probe_endpoints: string[];
  notes: string;
};

export type TenantConnection = {
  connection_id: string;
  group_id?: string;
  collections?: string[];
  connections?: TenantConnection[];
  connector: string;
  base_url: string;
  collection: string;
  scope: string;
  generation: string;
  auth_mode: string;
  credential_configured: boolean;
  ca_configured: boolean;
  mtls_configured: boolean;
  page_size: number;
  max_pages: number;
  max_records: number;
  interval_minutes: number;
  enabled: boolean;
};

export type ConnectionProbeEndpoint = {
  endpoint: string;
  status: number;
  duration_ms: number;
  objects: number;
  error?: string;
};

export type ConnectionProbe = {
  connection_id: string;
  failed_endpoints: number;
  report: {
    base_url: string;
    auth_mode: string;
    credential_ref?: string;
    endpoints: ConnectionProbeEndpoint[];
  };
};
