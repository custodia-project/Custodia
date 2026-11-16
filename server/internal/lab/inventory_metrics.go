package lab

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"time"

	"custodia/server/internal/connectors"
	"custodia/server/internal/metrics"
)

// InventoryRun is a run reference shown next to a collection or a metric.
type InventoryRun struct {
	RunID       string `json:"run_id"`
	Status      string `json:"status"`
	CompletedAt string `json:"completed_at,omitempty"`
	// sortAt is the best available timestamp for ordering runs that have not
	// completed yet. It is never serialized.
	sortAt string
}

// inventoryPublication is the run that published the data a metric reads. The
// scope and generation are kept for the record query only: a completed run
// under a new scope does not retire rows from the previous one, so counting
// every active row would mix two baselines.
type inventoryPublication struct {
	InventoryRun
	scope, generation string
}

// InventoryCollection is one readable collection row of a connection.
type InventoryCollection struct {
	Collection   string `json:"collection"`
	ConnectionID string `json:"connection_id"`
	// State is completed or partial (state of the last publication), or
	// never_published.
	State       string        `json:"state"`
	Records     *int          `json:"records,omitempty"`
	Publication *InventoryRun `json:"publication,omitempty"`
	LastRun     *InventoryRun `json:"last_run,omitempty"`
}

// InventoryMetricSource is the publication one metric value was read from.
type InventoryMetricSource struct {
	Collection   string `json:"collection"`
	ConnectionID string `json:"connection_id,omitempty"`
	RunID        string `json:"run_id,omitempty"`
	Status       string `json:"status,omitempty"`
	CompletedAt  string `json:"completed_at,omitempty"`
}

// InventoryMetric is one declared metric with its value for a connection.
type InventoryMetric struct {
	metrics.Definition
	Status      string                  `json:"status"`
	Value       *float64                `json:"value"`
	Partial     bool                    `json:"partial"`
	Numerator   *int                    `json:"numerator,omitempty"`
	Denominator *int                    `json:"denominator,omitempty"`
	Reason      string                  `json:"reason,omitempty"`
	Skipped     int                     `json:"skipped,omitempty"`
	DataAsOf    string                  `json:"data_as_of,omitempty"`
	Sources     []InventoryMetricSource `json:"sources"`
}

// InventoryTool is one connected tool (a connection group) visible to the
// reader. Only collections the reader can read are listed.
type InventoryTool struct {
	ConnectionID string                `json:"connection_id"`
	Connector    string                `json:"connector"`
	Name         string                `json:"name"`
	Vendor       string                `json:"vendor"`
	Category     string                `json:"category"`
	Completeness string                `json:"completeness"`
	LastRun      *InventoryRun         `json:"last_run,omitempty"`
	Collections  []InventoryCollection `json:"collections"`
	Metrics      []InventoryMetric     `json:"metrics"`
}

// InventoryRecord is one record composing a metric value.
type InventoryRecord struct {
	ExternalID   string            `json:"external_id"`
	Collection   string            `json:"collection"`
	ConnectionID string            `json:"connection_id"`
	Attributes   map[string]string `json:"attributes"`
}

// InventoryMetricRecords is the drill-down of one metric value.
type InventoryMetricRecords struct {
	ConnectionID string            `json:"connection_id"`
	Metric       InventoryMetric   `json:"metric"`
	Records      []InventoryRecord `json:"records"`
	Total        int               `json:"total"`
	Limit        int               `json:"limit"`
	Truncated    bool              `json:"truncated"`
}

type inventoryRow struct {
	connectionID, group, connector, collection string
	publication                                *inventoryPublication
	lastRun                                    *InventoryRun
	records                                    []metrics.Record
	loaded                                     bool
}

// readableInventoryRows returns the enabled sync rows the principal may read,
// with their last publication and last run. Everything happens inside the
// caller's read transaction; rows without a grant are never selected.
func readableInventoryRows(ctx context.Context, tx *sql.Tx, principalID int) ([]*inventoryRow, error) {
	rows, err := tx.QueryContext(ctx, `SELECT s.connection_id,
		CASE WHEN coalesce(s.group_id,'')='' THEN s.connection_id ELSE s.group_id END,
		s.connector,s.collection
		FROM lab_sync_sources s
		JOIN lab_grants g ON g.connection_id=s.connection_id
		JOIN lab_principals p ON p.id=g.principal_id AND p.enabled=1
		WHERE p.id=? AND s.enabled=1 ORDER BY s.connection_id`, principalID)
	if err != nil {
		return nil, err
	}
	result := []*inventoryRow{}
	for rows.Next() {
		row := &inventoryRow{}
		if err = rows.Scan(&row.connectionID, &row.group, &row.connector, &row.collection); err != nil {
			rows.Close()
			return nil, err
		}
		result = append(result, row)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, row := range result {
		var run inventoryPublication
		err = tx.QueryRowContext(ctx, `SELECT run_id,status,coalesce(completed_at,''),scope,generation FROM lab_sync_runs
			WHERE connection_id=? AND collection=? AND status IN ('completed','partial')
			ORDER BY completed_at DESC,enqueued_at DESC,rowid DESC LIMIT 1`, row.connectionID, row.collection).Scan(&run.RunID, &run.Status, &run.CompletedAt, &run.scope, &run.generation)
		if err == nil {
			publication := run
			row.publication = &publication
		} else if err != sql.ErrNoRows {
			return nil, err
		}
		var last InventoryRun
		err = tx.QueryRowContext(ctx, `SELECT run_id,status,coalesce(completed_at,''),coalesce(completed_at,started_at,enqueued_at) FROM lab_sync_runs
			WHERE connection_id=? ORDER BY coalesce(completed_at,started_at,enqueued_at) DESC,rowid DESC LIMIT 1`, row.connectionID).Scan(&last.RunID, &last.Status, &last.CompletedAt, &last.sortAt)
		if err == nil {
			row.lastRun = &last
		} else if err != sql.ErrNoRows {
			return nil, err
		}
	}
	return result, nil
}

// loadRecords reads the active observations of a published row once. It is
// restricted to the scope and generation of the selected publication: rows
// from an earlier scope stay active but are not part of this publication.
func (row *inventoryRow) loadRecords(ctx context.Context, tx *sql.Tx) error {
	if row.loaded || row.publication == nil {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT external_id,document FROM lab_observations
		WHERE connection_id=? AND collection=? AND status='active' AND scope=? AND generation=? ORDER BY row_id`,
		row.connectionID, row.collection, row.publication.scope, row.publication.generation)
	if err != nil {
		return err
	}
	defer rows.Close()
	row.records = []metrics.Record{}
	for rows.Next() {
		var record metrics.Record
		var document []byte
		if err = rows.Scan(&record.ExternalID, &document); err != nil {
			return err
		}
		record.Attributes = map[string]string{}
		if err = json.Unmarshal(document, &record.Attributes); err != nil {
			return err
		}
		row.records = append(row.records, record)
	}
	row.loaded = true
	return rows.Err()
}

type inventoryGroup struct {
	id, connector string
	byCollection  map[string]*inventoryRow
	order         []*inventoryRow
}

func groupInventoryRows(rows []*inventoryRow) []*inventoryGroup {
	groups := map[string]*inventoryGroup{}
	ordered := []*inventoryGroup{}
	for _, row := range rows {
		group := groups[row.group]
		if group == nil {
			group = &inventoryGroup{id: row.group, connector: row.connector, byCollection: map[string]*inventoryRow{}}
			groups[row.group] = group
			ordered = append(ordered, group)
		}
		if row.connector != group.connector {
			// A group belongs to one connector. A mismatched row is not
			// merged into another tool's metrics.
			continue
		}
		if _, duplicate := group.byCollection[row.collection]; duplicate {
			continue
		}
		group.byCollection[row.collection] = row
		group.order = append(group.order, row)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].id < ordered[j].id })
	return ordered
}

// evaluateInventoryMetric computes one metric for a group. Collections the
// reader cannot read are absent from byCollection and therefore report
// collection_unavailable, the same as a collection that is not connected.
func evaluateInventoryMetric(ctx context.Context, tx *sql.Tx, group *inventoryGroup, definition metrics.Definition) (InventoryMetric, metrics.Result, error) {
	snapshots := map[string]metrics.Snapshot{}
	metric := InventoryMetric{Definition: definition, Sources: []InventoryMetricSource{}}
	oldest := ""
	for _, collection := range definition.Collections() {
		row := group.byCollection[collection]
		if row == nil {
			metric.Sources = append(metric.Sources, InventoryMetricSource{Collection: collection})
			continue
		}
		source := InventoryMetricSource{Collection: collection, ConnectionID: row.connectionID}
		snapshot := metrics.Snapshot{Connected: true}
		if row.publication != nil {
			if err := row.loadRecords(ctx, tx); err != nil {
				return metric, metrics.Result{}, err
			}
			source.RunID, source.Status, source.CompletedAt = row.publication.RunID, row.publication.Status, row.publication.CompletedAt
			snapshot.Published = true
			snapshot.Partial = row.publication.Status == "partial"
			snapshot.PublishedAt, _ = time.Parse(time.RFC3339, row.publication.CompletedAt)
			snapshot.Records = row.records
			if oldest == "" || row.publication.CompletedAt < oldest {
				oldest = row.publication.CompletedAt
			}
		}
		snapshots[collection] = snapshot
		metric.Sources = append(metric.Sources, source)
	}
	result := metrics.Evaluate(definition, snapshots)
	metric.Status = result.Status
	metric.Value = result.Value
	metric.Numerator = result.Numerator
	metric.Denominator = result.Denominator
	metric.Reason = result.Reason
	metric.Skipped = result.Skipped
	// Partial means the displayed value was computed over an incomplete
	// publication; an unavailable value carries no partial claim.
	metric.Partial = result.Status == metrics.StatusPartial
	if result.Status != metrics.StatusUnavailable || result.Reason == metrics.ReasonEmptyDenominator {
		metric.DataAsOf = oldest
	}
	return metric, result, nil
}

func inventoryTool(ctx context.Context, tx *sql.Tx, group *inventoryGroup) (InventoryTool, error) {
	tool := InventoryTool{ConnectionID: group.id, Connector: group.connector, Name: group.connector, Collections: []InventoryCollection{}, Metrics: []InventoryMetric{}}
	if descriptor, found := connectors.Get(group.connector); found {
		tool.Name, tool.Vendor, tool.Category = descriptor.Name, descriptor.Vendor, descriptor.Category
	}
	published, partial := 0, 0
	for _, row := range group.order {
		collection := InventoryCollection{Collection: row.collection, ConnectionID: row.connectionID, State: "never_published", LastRun: row.lastRun}
		if row.publication != nil {
			if err := row.loadRecords(ctx, tx); err != nil {
				return tool, err
			}
			publication := row.publication.InventoryRun
			collection.Publication = &publication
			count := len(row.records)
			collection.Records = &count
			collection.State = row.publication.Status
			published++
			if row.publication.Status == "partial" {
				partial++
			}
		}
		if row.lastRun != nil && (tool.LastRun == nil || runStamp(row.lastRun) > runStamp(tool.LastRun)) {
			last := *row.lastRun
			tool.LastRun = &last
		}
		tool.Collections = append(tool.Collections, collection)
	}
	switch {
	case published == 0:
		tool.Completeness = "unpublished"
	case published < len(group.order):
		tool.Completeness = "incomplete"
	case partial > 0:
		tool.Completeness = "partial"
	default:
		tool.Completeness = "complete"
	}
	for _, definition := range metrics.ForConnector(group.connector) {
		metric, _, err := evaluateInventoryMetric(ctx, tx, group, definition)
		if err != nil {
			return tool, err
		}
		tool.Metrics = append(tool.Metrics, metric)
	}
	return tool, nil
}

func runStamp(run *InventoryRun) string {
	stamp := run.CompletedAt
	if stamp == "" {
		stamp = run.sortAt
	}
	return stamp + "|" + run.RunID
}

// InventoryTools returns the connected tools visible to the principal with
// their declared metrics computed over the last publication of each
// collection. A principal without a read grant sees neither the connection
// nor its metrics.
func (s *Store) InventoryTools(ctx context.Context, p Principal) ([]InventoryTool, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := readableInventoryRows(ctx, tx, p.ID)
	if err != nil {
		return nil, err
	}
	tools := []InventoryTool{}
	for _, group := range groupInventoryRows(rows) {
		tool, err := inventoryTool(ctx, tx, group)
		if err != nil {
			return nil, err
		}
		tools = append(tools, tool)
	}
	return tools, tx.Commit()
}

// InventoryMetricRecords returns exactly the records that compose one metric
// value for a visible connection. A hidden connection, an unknown metric or a
// metric of another connector all return ErrSyncNotFound.
func (s *Store) InventoryMetricRecords(ctx context.Context, p Principal, metricID, connectionID string, limit int) (InventoryMetricRecords, error) {
	if limit < 1 || limit > 1000 {
		limit = 200
	}
	definition, found := metrics.Get(metricID)
	if !found {
		return InventoryMetricRecords{}, ErrSyncNotFound
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return InventoryMetricRecords{}, err
	}
	defer tx.Rollback()
	rows, err := readableInventoryRows(ctx, tx, p.ID)
	if err != nil {
		return InventoryMetricRecords{}, err
	}
	var group *inventoryGroup
	for _, candidate := range groupInventoryRows(rows) {
		if candidate.id == connectionID {
			group = candidate
		}
	}
	if group == nil || group.connector != definition.Connector {
		return InventoryMetricRecords{}, ErrSyncNotFound
	}
	metric, result, err := evaluateInventoryMetric(ctx, tx, group, definition)
	if err != nil {
		return InventoryMetricRecords{}, err
	}
	response := InventoryMetricRecords{ConnectionID: group.id, Metric: metric, Records: []InventoryRecord{}, Limit: limit}
	if metric.Value != nil {
		origin := group.byCollection[definition.Collection]
		response.Total = len(result.Matched)
		for i, record := range result.Matched {
			if i >= limit {
				response.Truncated = true
				break
			}
			response.Records = append(response.Records, InventoryRecord{
				ExternalID: record.ExternalID, Collection: definition.Collection,
				ConnectionID: origin.connectionID, Attributes: record.Attributes,
			})
		}
	}
	return response, tx.Commit()
}
