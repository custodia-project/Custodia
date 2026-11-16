package lab

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestReplayPersistsAndIsAtomicAcrossProcesses(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "replay.db")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	if err = a.ConsumeAssertion(ctx, "issuer", "used", now+65, now); err != nil {
		t.Fatal(err)
	}
	a.DB.Close()
	a, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.DB.Close()
	if err = a.ConsumeAssertion(ctx, "issuer", "used", now+65, now); err != ErrReplay {
		t.Fatal("replay survived restart", err)
	}
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.DB.Close()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, store := range []*Store{a, b} {
		wg.Add(1)
		go func(s *Store) { defer wg.Done(); results <- s.ConsumeAssertion(ctx, "issuer", "racing", now+65, now) }(store)
	}
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatal("duplicate accepted concurrently", accepted)
	}
	if err = a.ConsumeAssertion(ctx, "issuer", "used", now+130, now+66); err != nil {
		t.Fatal("expired replay entry retained", err)
	}
}

func TestAdministratorBootstrapAndAuditRollback(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "admin.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	// An empty installation has neither laboratory users nor default credentials.
	var count int
	s.DB.QueryRow("SELECT count(*) FROM lab_principals").Scan(&count)
	if count != 0 {
		t.Fatal("default user created")
	}
	if err = s.BootstrapAdministrator(ctx, "https://login.acme.example", "sub-admin", []string{"unknown"}); err == nil {
		t.Fatal("unknown source granted")
	}
	if err = s.BootstrapAdministrator(ctx, "https://login.acme.example", "sub-admin", nil); err != nil {
		t.Fatal(err)
	}
	actor, err := s.ResolvePrincipal(ctx, "https://login.acme.example", "sub-admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`CREATE TRIGGER reject_access_audit BEFORE INSERT ON lab_audit BEGIN SELECT RAISE(ABORT,'test'); END`); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateReader(ctx, actor, "sub-reader", nil); err == nil {
		t.Fatal("unaudited reader created")
	}
	s.DB.QueryRow("SELECT count(*) FROM lab_principals WHERE subject='sub-reader'").Scan(&count)
	if count != 0 {
		t.Fatal("reader transaction partially committed")
	}
	if err = s.BootstrapAdministrator(ctx, "https://login.acme.example", "second", nil); err == nil {
		t.Fatal("bootstrap repeated")
	}
}

func TestReplayCapacityFailsClosedAndExpires(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "capacity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	if _, err = s.DB.Exec(`WITH RECURSIVE ids(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM ids WHERE n<?) INSERT INTO gateway_replay SELECT 'issuer',CAST(n AS TEXT),? FROM ids`, ReplayCapacity, now+65); err != nil {
		t.Fatal(err)
	}
	if err = s.ConsumeAssertion(ctx, "issuer", "new-request", now+65, now); err != ErrReplayCapacity {
		t.Fatal("unbounded replay store", err)
	}
	if err = s.ConsumeAssertion(ctx, "issuer", "new-request", now+130, now+66); err != nil {
		t.Fatal("expired capacity retained", err)
	}
}
