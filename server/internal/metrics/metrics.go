// Package metrics declares, per connector, the inventory metrics Custodia
// computes over already-published observations. Definitions live in the
// binary next to the connector registry; an administrator cannot define them
// in the MVP. Changing a calculation means a new versioned ID: a historical
// value is never reinterpreted.
//
// The package is pure: it has no SQL or HTTP. The caller supplies one
// snapshot per source collection (last publication, partial flag, active
// records) already filtered by the reader's grants.
package metrics

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"time"

	"custodia/server/internal/connectors"
	"custodia/server/internal/inventory"
)

// Calculation kinds. Every kind is explainable from the records it counts.
const (
	// KindCount counts the active records of the collection.
	KindCount = "count"
	// KindCountWhere counts records whose allowlisted attribute equals one of
	// the declared values.
	KindCountWhere = "count_where"
	// KindRatio is Where (numerator) over Denominator (or every record), as a
	// percentage. It is never computed over a partial publication.
	KindRatio = "ratio"
	// KindAgeOver counts records whose RFC3339 attribute is older than a fixed
	// threshold, measured from the publication time (not the request time).
	KindAgeOver = "age_over"
	// KindCountWithout counts origin records that no record of a sibling
	// collection of the same connection references. It is not computed when
	// the referencing collection is partial: missing children would inflate it.
	KindCountWithout = "count_without"
)

// Result statuses.
const (
	StatusAvailable   = "available"
	StatusPartial     = "partial"
	StatusUnavailable = "unavailable"
)

// Reasons explain an unavailable or withheld value. They are stable codes.
const (
	// ReasonCollectionUnavailable: the collection is not connected in this
	// connection or the reader cannot read it. Both are reported the same way
	// so a reader cannot learn that a hidden connection exists.
	ReasonCollectionUnavailable = "collection_unavailable"
	// ReasonNoPublication: no completed or partial run has published data.
	ReasonNoPublication = "no_publication"
	// ReasonPartialDenominator: a ratio over a partial publication.
	ReasonPartialDenominator = "partial_denominator"
	// ReasonPartialReference: the referencing collection is partial.
	ReasonPartialReference = "partial_reference"
	// ReasonEmptyDenominator: the confirmed denominator is zero.
	ReasonEmptyDenominator = "empty_denominator"
)

// Condition matches a record when Field (an allowlisted attribute of the
// collection) equals one of Values exactly. A missing attribute never matches.
type Condition struct {
	Field  string   `json:"field"`
	Values []string `json:"values"`
}

func (c *Condition) matches(record Record) bool {
	if c == nil {
		return true
	}
	value, found := record.Attributes[c.Field]
	return found && slices.Contains(c.Values, value)
}

// Reference names the sibling collection and the attribute that points to the
// origin record's external ID.
type Reference struct {
	Collection string `json:"collection"`
	Field      string `json:"field"`
}

// Definition is one declared metric.
type Definition struct {
	ID            string     `json:"id"`
	Connector     string     `json:"connector"`
	Name          string     `json:"name"`
	Description   string     `json:"description"`
	Collection    string     `json:"collection"`
	Kind          string     `json:"kind"`
	Unit          string     `json:"unit"`
	Where         *Condition `json:"where,omitempty"`
	Denominator   *Condition `json:"denominator_where,omitempty"`
	AgeField      string     `json:"age_field,omitempty"`
	ThresholdDays int        `json:"threshold_days,omitempty"`
	Without       *Reference `json:"without,omitempty"`
}

// Collections returns every source collection of the metric, origin first.
func (d Definition) Collections() []string {
	result := []string{d.Collection}
	if d.Without != nil {
		result = append(result, d.Without.Collection)
	}
	return result
}

// Record is one active observation.
type Record struct {
	ExternalID string
	Attributes map[string]string
}

// Snapshot is the reader-visible state of one source collection.
type Snapshot struct {
	// Connected is false when the collection is not configured in this
	// connection or the reader has no read grant on it.
	Connected bool
	// Published is true when a completed or partial run published data.
	Published bool
	Partial   bool
	// PublishedAt is the completion time of that publication.
	PublishedAt time.Time
	Records     []Record
}

// Result is one computed value. Matched holds exactly the records that
// compose the value (the numerator for a ratio) for the drill-down.
type Result struct {
	Status      string
	Value       *float64
	Numerator   *int
	Denominator *int
	Reason      string
	// Skipped counts records without a usable value for an age metric.
	Skipped int
	Matched []Record
}

// Evaluate computes one metric over the snapshots of its collections. It
// never turns missing data into zero: zero is returned only from a published
// collection with no matching record.
func Evaluate(d Definition, snapshots map[string]Snapshot) Result {
	for _, collection := range d.Collections() {
		if !snapshots[collection].Connected {
			return Result{Status: StatusUnavailable, Reason: ReasonCollectionUnavailable}
		}
	}
	for _, collection := range d.Collections() {
		if !snapshots[collection].Published {
			return Result{Status: StatusUnavailable, Reason: ReasonNoPublication}
		}
	}
	origin := snapshots[d.Collection]
	status := StatusAvailable
	if origin.Partial {
		status = StatusPartial
	}
	result := Result{Status: status}
	switch d.Kind {
	case KindCount:
		result.Matched = filter(origin.Records, nil)
	case KindCountWhere:
		result.Matched = filter(origin.Records, d.Where)
	case KindAgeOver:
		limit := origin.PublishedAt.Add(-time.Duration(d.ThresholdDays) * 24 * time.Hour)
		for _, record := range origin.Records {
			stamp, err := time.Parse(time.RFC3339, record.Attributes[d.AgeField])
			if err != nil {
				result.Skipped++
				continue
			}
			if stamp.Before(limit) {
				result.Matched = append(result.Matched, record)
			}
		}
	case KindCountWithout:
		reference := snapshots[d.Without.Collection]
		if reference.Partial {
			// A partial child set would report Safes without members that
			// actually have them: withhold the value instead.
			return Result{Status: StatusPartial, Reason: ReasonPartialReference}
		}
		referenced := map[string]bool{}
		for _, record := range reference.Records {
			referenced[record.Attributes[d.Without.Field]] = true
		}
		for _, record := range origin.Records {
			if !referenced[record.ExternalID] {
				result.Matched = append(result.Matched, record)
			}
		}
	case KindRatio:
		if origin.Partial {
			return Result{Status: StatusPartial, Reason: ReasonPartialDenominator}
		}
		denominator := 0
		for _, record := range origin.Records {
			if d.Denominator.matches(record) {
				denominator++
				if d.Where.matches(record) {
					result.Matched = append(result.Matched, record)
				}
			}
		}
		numerator := len(result.Matched)
		result.Numerator, result.Denominator = &numerator, &denominator
		if denominator == 0 {
			result.Status = StatusUnavailable
			result.Reason = ReasonEmptyDenominator
			result.Matched = nil
			return result
		}
		value := math.Round(float64(numerator)*1000/float64(denominator)) / 10
		result.Value = &value
		return result
	default:
		return Result{Status: StatusUnavailable, Reason: "unsupported_kind"}
	}
	if result.Matched == nil {
		result.Matched = []Record{}
	}
	value := float64(len(result.Matched))
	result.Value = &value
	return result
}

func filter(records []Record, condition *Condition) []Record {
	result := []Record{}
	for _, record := range records {
		if condition.matches(record) {
			result = append(result, record)
		}
	}
	return result
}

// All returns every declared metric in declaration order.
func All() []Definition {
	return slices.Clone(declared)
}

// ForConnector returns the metrics declared for one connector.
func ForConnector(connector string) []Definition {
	result := []Definition{}
	for _, definition := range declared {
		if definition.Connector == connector {
			result = append(result, definition)
		}
	}
	return result
}

// Get returns one metric by its versioned ID.
func Get(id string) (Definition, bool) {
	for _, definition := range declared {
		if definition.ID == id {
			return definition, true
		}
	}
	return Definition{}, false
}

var versionedID = regexp.MustCompile(`^[a-z0-9-]+(\.[a-z0-9_]+)+\.v[1-9][0-9]*$`)

// Validate checks every declaration against the connector registry and the
// collection allowlists, so a metric can only read fields an adapter stores.
func Validate(definitions []Definition) error {
	seen := map[string]bool{}
	for _, d := range definitions {
		if !versionedID.MatchString(d.ID) {
			return fmt.Errorf("metric %q: ID must be versioned (…vN)", d.ID)
		}
		if seen[d.ID] {
			return fmt.Errorf("metric %q: duplicate ID", d.ID)
		}
		seen[d.ID] = true
		if d.Name == "" || d.Description == "" || d.Unit == "" {
			return fmt.Errorf("metric %q: name, description and unit are required", d.ID)
		}
		descriptor, found := connectors.Get(d.Connector)
		if !found || !connectors.Connectable(descriptor) {
			return fmt.Errorf("metric %q: connector %q is not implemented", d.ID, d.Connector)
		}
		for _, collection := range d.Collections() {
			if !connectors.SupportsCollection(descriptor, collection) {
				return fmt.Errorf("metric %q: connector does not declare collection %q", d.ID, collection)
			}
		}
		allowed := func(collection, field string) error {
			spec, found := inventory.CollectionFor(collection)
			if !found || !spec.Allowed[field] {
				return fmt.Errorf("metric %q: %q is not an allowlisted field of %q", d.ID, field, collection)
			}
			return nil
		}
		checkCondition := func(condition *Condition, required bool) error {
			if condition == nil {
				if required {
					return fmt.Errorf("metric %q: condition required", d.ID)
				}
				return nil
			}
			if len(condition.Values) == 0 {
				return fmt.Errorf("metric %q: condition needs values", d.ID)
			}
			return allowed(d.Collection, condition.Field)
		}
		var err error
		unexpected := func(fields ...bool) error {
			for _, set := range fields {
				if set {
					return fmt.Errorf("metric %q: parameters do not match kind %q", d.ID, d.Kind)
				}
			}
			return nil
		}
		switch d.Kind {
		case KindCount:
			if d.Where != nil || d.Denominator != nil || d.Without != nil || d.AgeField != "" || d.ThresholdDays != 0 {
				err = fmt.Errorf("metric %q: count takes no parameters", d.ID)
			}
		case KindCountWhere:
			if err = checkCondition(d.Where, true); err == nil {
				err = unexpected(d.Denominator != nil, d.Without != nil, d.AgeField != "", d.ThresholdDays != 0)
			}
		case KindRatio:
			if err = checkCondition(d.Where, true); err == nil {
				err = checkCondition(d.Denominator, false)
			}
			if err == nil {
				err = unexpected(d.Without != nil, d.AgeField != "", d.ThresholdDays != 0)
			}
		case KindAgeOver:
			if d.ThresholdDays < 1 {
				err = fmt.Errorf("metric %q: age threshold must be at least one day", d.ID)
			} else {
				err = allowed(d.Collection, d.AgeField)
			}
			if err == nil {
				err = unexpected(d.Where != nil, d.Denominator != nil, d.Without != nil)
			}
		case KindCountWithout:
			if d.Without == nil || d.Without.Collection == d.Collection {
				err = fmt.Errorf("metric %q: count_without needs a sibling collection", d.ID)
			} else {
				err = allowed(d.Without.Collection, d.Without.Field)
			}
			if err == nil {
				err = unexpected(d.Where != nil, d.Denominator != nil, d.AgeField != "", d.ThresholdDays != 0)
			}
		default:
			err = fmt.Errorf("metric %q: unsupported kind %q", d.ID, d.Kind)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
