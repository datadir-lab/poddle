package audit

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openTmp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestNewEvent_StripsQueryAndHostOnly(t *testing.T) {
	e := NewEvent(Event{
		Pod:      "proj",
		Kind:     KindRequest,
		Upstream: "https://api.anthropic.com/v1/messages",
		Path:     "/v1/messages?token=sk-secret-abc&x=1",
	})
	if e.Path != "/v1/messages" {
		t.Errorf("query-string must be stripped from Path, got %q", e.Path)
	}
	if e.Upstream != "api.anthropic.com" {
		t.Errorf("Upstream must be host-only, got %q", e.Upstream)
	}
}

func TestStore_AppendChainsHashes(t *testing.T) {
	s := openTmp(t)
	var prev string
	for i := 0; i < 3; i++ {
		e, err := s.Append(NewEvent(Event{Pod: "p", Kind: KindRequest, Upstream: "h"}))
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		if e.Seq != int64(i+1) {
			t.Errorf("seq = %d, want %d", e.Seq, i+1)
		}
		if e.PrevHash != prev {
			t.Errorf("row %d PrevHash = %q, want %q", i, e.PrevHash, prev)
		}
		if e.Hash == "" || e.Hash == prev {
			t.Errorf("row %d hash not chained: %q", i, e.Hash)
		}
		prev = e.Hash
	}
	if ok, at, err := s.Verify(); err != nil || !ok {
		t.Fatalf("verify should pass on an untampered chain: ok=%v at=%d err=%v", ok, at, err)
	}
}

func TestStore_VerifyDetectsTampering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.Append(NewEvent(Event{Pod: "p", Kind: KindRequest})); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.Close()

	// Tamper with row 2's content out-of-band, leaving its hash intact.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE events SET detail = 'tampered' WHERE seq = 2"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	ok, at, err := s2.Verify()
	if err != nil {
		t.Fatal(err)
	}
	if ok || at != 2 {
		t.Errorf("verify should catch the edit at seq 2; ok=%v at=%d", ok, at)
	}
}

func TestStore_QueryFilters(t *testing.T) {
	s := openTmp(t)
	must := func(e Event) {
		if _, err := s.Append(NewEvent(e)); err != nil {
			t.Fatal(err)
		}
	}
	must(Event{Pod: "a", Kind: KindRequest, Decision: DecisionAllow})
	must(Event{Pod: "b", Kind: KindRedact, Decision: DecisionRedact})
	must(Event{Pod: "a", Kind: KindBlock, Decision: DecisionBlock})

	byPod, err := s.Query(Filter{Pod: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byPod) != 2 {
		t.Fatalf("pod=a should match 2 events, got %d", len(byPod))
	}
	if byPod[0].Seq < byPod[1].Seq {
		t.Errorf("query should be newest-first: %d then %d", byPod[0].Seq, byPod[1].Seq)
	}
	if blocked, _ := s.Query(Filter{Decision: string(DecisionBlock)}); len(blocked) != 1 || blocked[0].Pod != "a" {
		t.Errorf("decision=block should match the one block event, got %v", blocked)
	}
	if since, _ := s.Query(Filter{SinceSeq: 2}); len(since) != 1 || since[0].Seq != 3 {
		t.Errorf("since=2 should return only seq 3, got %v", since)
	}
}

func TestStore_SourceRecordedAndChained(t *testing.T) {
	s := openTmp(t)
	if _, err := s.Append(NewEvent(Event{Pod: "p", Kind: KindRequest, Source: "host-a"})); err != nil {
		t.Fatal(err)
	}
	got, err := s.Query(Filter{Pod: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Source != "host-a" {
		t.Fatalf("Source should round-trip through the store, got %+v", got)
	}
	if ok, at, _ := s.Verify(); !ok {
		t.Errorf("chain should verify with a Source set (broken at %d)", at)
	}
}

func TestStore_Subscribe(t *testing.T) {
	s := openTmp(t)
	ch, cancel := s.Subscribe()
	if _, err := s.Append(NewEvent(Event{Pod: "p", Kind: KindRequest})); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-ch:
		if e.Pod != "p" {
			t.Errorf("subscriber got %+v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber did not receive the appended event")
	}
	cancel() // after unsubscribe, no more deliveries (and no panic on further appends)
	if _, err := s.Append(NewEvent(Event{Pod: "q", Kind: KindRequest})); err != nil {
		t.Fatal(err)
	}
}
