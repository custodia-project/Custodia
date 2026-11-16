/** Contract of GET /api/inventory/tools and its metric drill-down (Go backend,
 *  server/internal/lab/inventory_http.go). Metrics are declared per connector
 *  in the binary; the reader only sees connections it can read. */

export type InventoryRun = { run_id: string; status: string; completed_at?: string };

export type InventoryCollection = {
  collection: string;
  connection_id: string;
  state: 'completed' | 'partial' | 'never_published';
  records?: number;
  publication?: InventoryRun;
  last_run?: InventoryRun;
};

export type InventoryMetricKind = 'count' | 'count_where' | 'ratio' | 'age_over' | 'count_without';
export type InventoryMetricStatus = 'available' | 'partial' | 'unavailable';
export type InventoryMetricReason =
  | 'collection_unavailable' | 'no_publication' | 'partial_denominator'
  | 'partial_reference' | 'empty_denominator';

export type InventoryMetricSource = {
  collection: string;
  connection_id?: string;
  run_id?: string;
  status?: string;
  completed_at?: string;
};

export type InventoryCondition = { field: string; values: string[] };

export type InventoryMetric = {
  id: string;
  connector: string;
  name: string;
  description: string;
  collection: string;
  kind: InventoryMetricKind;
  unit: string;
  where?: InventoryCondition;
  denominator_where?: InventoryCondition;
  age_field?: string;
  threshold_days?: number;
  without?: { collection: string; field: string };
  status: InventoryMetricStatus;
  /** null whenever the value is not available or withheld; 0 only when confirmed. */
  value: number | null;
  partial: boolean;
  numerator?: number;
  denominator?: number;
  reason?: InventoryMetricReason | string;
  skipped?: number;
  data_as_of?: string;
  sources: InventoryMetricSource[];
};

export type InventoryTool = {
  connection_id: string;
  connector: string;
  name: string;
  vendor: string;
  category: string;
  completeness: 'complete' | 'partial' | 'incomplete' | 'unpublished';
  last_run?: InventoryRun;
  collections: InventoryCollection[];
  metrics: InventoryMetric[];
};

export type InventoryToolsResponse = { generated_at: string; tools: InventoryTool[] };

export type InventoryRecord = {
  external_id: string;
  collection: string;
  connection_id: string;
  attributes: Record<string, string>;
};

export type InventoryMetricRecords = {
  connection_id: string;
  metric: InventoryMetric;
  records: InventoryRecord[];
  total: number;
  limit: number;
  truncated: boolean;
};
