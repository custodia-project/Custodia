package lab

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"
)

const IdentityConnection = "acme-identity"
const NetskopeConnection = "acme-netskope"

var ErrDenied = errors.New("source access denied")

type Principal struct {
	ID              int
	Issuer, Subject string
}

func (p Principal) Actor() string { return fmt.Sprintf("principal:%d", p.ID) }

type Connection struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Connector string `json:"connector,omitempty"`
}
type record struct {
	Name   string `json:"name,omitempty"`
	Email  string `json:"email,omitempty"`
	UserID string `json:"user_id,omitempty"`
	Last   string `json:"last_signal_at,omitempty"`
}
type sourceRun struct{ Connection, Kind, Status, Completed string }

// Migration 3 is additive. Applied migration 1/2 statements stay unchanged.
func migrateObservations(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS lab_connections(id TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK(kind IN ('identity','netskope')));
 CREATE TABLE IF NOT EXISTS lab_runs(connection_id TEXT PRIMARY KEY REFERENCES lab_connections(id),status TEXT NOT NULL,completed_at TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS lab_observations(row_id INTEGER PRIMARY KEY, connection_id TEXT NOT NULL REFERENCES lab_connections(id),collection TEXT NOT NULL,external_id TEXT NOT NULL,document BLOB NOT NULL, UNIQUE(connection_id,collection,external_id));
 CREATE TABLE IF NOT EXISTS lab_principals(id INTEGER PRIMARY KEY,issuer TEXT NOT NULL,subject TEXT NOT NULL,enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),UNIQUE(issuer,subject));
 CREATE TABLE IF NOT EXISTS lab_grants(principal_id INTEGER NOT NULL REFERENCES lab_principals(id),connection_id TEXT NOT NULL REFERENCES lab_connections(id),PRIMARY KEY(principal_id,connection_id));
 CREATE TABLE IF NOT EXISTS lab_credentials(digest TEXT PRIMARY KEY,principal_id INTEGER NOT NULL REFERENCES lab_principals(id));
 CREATE TABLE IF NOT EXISTS lab_correlation_rules(identity_id TEXT NOT NULL REFERENCES lab_connections(id),netskope_id TEXT NOT NULL REFERENCES lab_connections(id),version TEXT NOT NULL,PRIMARY KEY(identity_id,netskope_id));`)
	if err != nil {
		return err
	}
	var version int
	if err = tx.QueryRowContext(ctx, "SELECT max(version) FROM lab_schema_migrations").Scan(&version); err != nil {
		return err
	}
	if version < 3 {
		var b []byte
		err = tx.QueryRowContext(ctx, "SELECT document FROM lab_snapshot WHERE id=1").Scan(&b)
		if err == nil {
			var c Company
			if err = json.Unmarshal(b, &c); err != nil {
				return err
			}
			if err = normalize(ctx, tx, c); err != nil {
				return err
			}
		} else if err != sql.ErrNoRows {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO lab_schema_migrations VALUES(3)")
	}
	return err
}

func normalize(ctx context.Context, tx *sql.Tx, c Company) error {
	if _, err := time.Parse(time.RFC3339, c.Company.Captured); err != nil {
		return fmt.Errorf("invalid lab capture")
	}
	for _, connection := range []Connection{{ID: IdentityConnection, Kind: "identity"}, {ID: NetskopeConnection, Kind: "netskope"}} {
		if _, err := tx.ExecContext(ctx, "INSERT INTO lab_connections VALUES(?,?)", connection.ID, connection.Kind); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO lab_runs VALUES(?,'completed',?)", connection.ID, c.Company.Captured); err != nil {
			return err
		}
	}
	insert := func(connection, collection, id string, r record) error {
		b, err := json.Marshal(r)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO lab_observations(connection_id,collection,external_id,document) VALUES(?,?,?,?)", connection, collection, id, b)
		return err
	}
	users := map[int]string{}
	for _, p := range c.People {
		for _, id := range p.IdentityIDs {
			if err := insert(IdentityConnection, "users", strconv.Itoa(id), record{Name: p.Name, Email: p.Email}); err != nil {
				return err
			}
		}
		if p.NetskopeID != 0 {
			users[p.ID] = strconv.Itoa(p.NetskopeID)
			if err := insert(NetskopeConnection, "users", strconv.Itoa(p.NetskopeID), record{Name: p.Name, Email: p.Email}); err != nil {
				return err
			}
		}
	}
	for i, d := range c.Devices {
		if users[d.PersonID] == "" {
			return fmt.Errorf("device without source user")
		}
		external := d.ExternalID
		if external == "" {
			external = fmt.Sprintf("lab-legacy-device-%d", i+1)
		}
		if err := insert(NetskopeConnection, "devices", external, record{UserID: users[d.PersonID], Last: d.Last}); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO lab_correlation_rules VALUES(?,?,'1')", IdentityConnection, NetskopeConnection); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO lab_principals(issuer,subject,enabled) VALUES('custodia-lab','synthetic-lab',1)"); err != nil {
		return err
	}
	var id int
	if err := tx.QueryRowContext(ctx, "SELECT id FROM lab_principals WHERE issuer='custodia-lab' AND subject='synthetic-lab'").Scan(&id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO lab_grants VALUES(?,?),(?,?)", id, IdentityConnection, id, NetskopeConnection); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "UPDATE lab_preferences SET actor=? WHERE actor='synthetic-lab'", (Principal{ID: id}).Actor())
	return err
}

func digest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
func (s *Store) principal(ctx context.Context, subject string) (Principal, error) {
	var p Principal
	err := s.DB.QueryRowContext(ctx, "SELECT id,issuer,subject FROM lab_principals WHERE issuer='custodia-lab' AND subject=? AND enabled=1", subject).Scan(&p.ID, &p.Issuer, &p.Subject)
	return p, err
}
func (s *Store) credential(ctx context.Context, token string) (Principal, error) {
	var p Principal
	err := s.DB.QueryRowContext(ctx, "SELECT p.id,p.issuer,p.subject FROM lab_credentials c JOIN lab_principals p ON p.id=c.principal_id WHERE c.digest=? AND p.enabled=1", digest(token)).Scan(&p.ID, &p.Issuer, &p.Subject)
	return p, err
}

// IssuePrincipal is a host-administrative operation, not a public API.
// A subject cannot silently acquire a different scope when a credential is issued.
func (s *Store) IssuePrincipal(ctx context.Context, subject, token string, connections []string) error {
	if subject == "" || subject == "synthetic-lab" || token == "" {
		return fmt.Errorf("invalid laboratory principal")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "INSERT INTO lab_principals(issuer,subject,enabled) VALUES('custodia-lab',?,1)", subject)
	if err != nil {
		return err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	for _, connection := range connections {
		if _, err = tx.ExecContext(ctx, "INSERT INTO lab_grants VALUES(?,?)", id, connection); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO lab_credentials VALUES(?,?)", digest(token), id); err != nil {
		return err
	}
	after, _ := json.Marshal(map[string]any{"issuer": "custodia-lab", "subject": subject, "connections": connections})
	if _, err = tx.ExecContext(ctx, "INSERT INTO lab_audit(actor,action,before_json,after_json,at) VALUES('host','issue_lab_principal','{}',?,?)", after, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) RevokePrincipal(ctx context.Context, subject string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "UPDATE lab_principals SET enabled=0 WHERE issuer='custodia-lab' AND subject=? AND enabled=1", subject)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrDenied
	}
	after, _ := json.Marshal(map[string]any{"subject": subject, "enabled": false})
	if _, err = tx.ExecContext(ctx, "INSERT INTO lab_audit(actor,action,before_json,after_json,at) VALUES('host','revoke_lab_principal','{}',?,?)", after, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Connections(ctx context.Context, p Principal) ([]Connection, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT c.id,c.kind,coalesce(s.connector,'') FROM lab_connections c JOIN lab_grants g ON g.connection_id=c.id JOIN lab_principals p ON p.id=g.principal_id LEFT JOIN lab_sync_sources s ON s.connection_id=c.id WHERE p.id=? AND p.enabled=1 ORDER BY c.id`, p.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Connection{}
	for rows.Next() {
		var c Connection
		if err = rows.Scan(&c.ID, &c.Kind, &c.Connector); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

// View reads observations only after joining grants, in a single read snapshot.
// The selected pair is explicit; no global email union across connections exists.
func (s *Store) View(ctx context.Context, p Principal, identity, netskope string, explicitIdentity, explicitNetskope, requireBoth bool) (Company, error) {
	c := Company{normalized: true, runs: map[string]sourceRun{}, sourceIDs: map[int]string{}, userCounts: map[int]int{}}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return c, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT c.id,c.kind FROM lab_connections c JOIN lab_grants g ON c.id=g.connection_id JOIN lab_principals p ON p.id=g.principal_id WHERE p.id=? AND p.enabled=1 AND c.id IN (?,?)`, p.ID, identity, netskope)
	if err != nil {
		return c, err
	}
	allowed := map[string]string{}
	for rows.Next() {
		var id, kind string
		if err = rows.Scan(&id, &kind); err != nil {
			rows.Close()
			return c, err
		}
		allowed[id] = kind
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return c, err
	}
	hasIdentity := allowed[identity] == "identity"
	hasNetskope := allowed[netskope] == "netskope"
	if (!hasIdentity && !hasNetskope) || (explicitIdentity && !hasIdentity) || (explicitNetskope && !hasNetskope) || (requireBoth && (!hasIdentity || !hasNetskope)) {
		return c, ErrDenied
	}
	if hasIdentity && hasNetskope {
		var version string
		if err = tx.QueryRowContext(ctx, "SELECT version FROM lab_correlation_rules WHERE identity_id=? AND netskope_id=?", identity, netskope).Scan(&version); err != nil || version != "1" {
			return c, ErrDenied
		}
	}
	for key, selection := range map[string]struct {
		id   string
		read bool
	}{"identity": {identity, hasIdentity}, "netskope": {netskope, hasNetskope}} {
		if !selection.read {
			continue
		}
		run := sourceRun{Connection: selection.id, Kind: key}
		err = tx.QueryRowContext(ctx, "SELECT status,completed_at FROM lab_runs WHERE connection_id=?", selection.id).Scan(&run.Status, &run.Completed)
		if err == sql.ErrNoRows {
			run.Status = "missing"
		} else if err != nil {
			return c, err
		}
		c.runs[key] = run
	}
	rows, err = tx.QueryContext(ctx, `SELECT o.row_id,o.connection_id,o.collection,o.external_id,o.document FROM lab_observations o JOIN lab_grants g ON g.connection_id=o.connection_id JOIN lab_principals p ON p.id=g.principal_id WHERE p.id=? AND p.enabled=1 AND o.status='active' AND o.connection_id IN (?,?) ORDER BY o.row_id`, p.ID, identity, netskope)
	if err != nil {
		return c, err
	}
	type observation struct {
		id                               int
		connection, collection, external string
		r                                record
	}
	observations := []observation{}
	for rows.Next() {
		var o observation
		var b []byte
		if err = rows.Scan(&o.id, &o.connection, &o.collection, &o.external, &b); err != nil {
			rows.Close()
			return c, err
		}
		if err = json.Unmarshal(b, &o.r); err != nil {
			rows.Close()
			return c, err
		}
		observations = append(observations, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return c, err
	}
	if err = tx.Commit(); err != nil {
		return c, err
	}
	people := map[string]*Person{}
	users := map[string]string{}
	for _, o := range observations {
		if o.collection != "users" {
			continue
		}
		person := people[o.r.Email]
		if person == nil {
			person = &Person{Email: o.r.Email, Name: o.r.Name, IdentityIDs: []int{}}
			people[o.r.Email] = person
		}
		c.sourceIDs[o.id] = o.external
		if o.connection == identity {
			person.IdentityIDs = append(person.IdentityIDs, o.id)
			person.Name = o.r.Name
		} else {
			person.NetskopeID = o.id
			users[o.external] = o.r.Email
		}
	}
	emails := []string{}
	for email := range people {
		emails = append(emails, email)
	}
	sort.Strings(emails)
	for i, email := range emails {
		p := people[email]
		p.ID = i + 1
		c.People = append(c.People, *p)
	}
	for _, o := range observations {
		if o.collection == "users" && o.connection == netskope {
			if person := people[o.r.Email]; person != nil {
				c.userCounts[person.ID]++
			}
		}
	}
	for _, o := range observations {
		if o.collection != "devices" || o.connection != netskope {
			continue
		}
		email, found := users[o.r.UserID]
		if !found {
			continue
		}
		p := people[email]
		if c.userCounts[p.ID] != 1 {
			continue
		}
		c.Devices = append(c.Devices, Device{PersonID: p.ID, Last: o.r.Last})
	}
	return c, nil
}
