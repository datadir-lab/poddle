// Package audit is poddle's tamper-evident, secret-free audit spine. The broker
// daemon is the single writer: every security-relevant event (a proxied request,
// a redaction/block, a handle issued/revoked, a pod's lifecycle) is appended to a
// hash-chained SQLite log and fanned out to live subscribers (the dashboard).
//
// Events carry NO secrets by construction — no bodies, no resolved credentials,
// no URLs with query-strings (which can carry tokens). NewEvent enforces this.
package audit

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // registers the pure-Go "sqlite" driver
)

// Kind classifies an audit event.
type Kind string

const (
	KindRequest       Kind = "request"        // a proxied request to a brokered upstream
	KindRedact        Kind = "redact"         // a secret was scrubbed from egress
	KindBlock         Kind = "block"          // egress blocked (secret detected)
	KindHandleIssue   Kind = "handle.issue"   // a pod-scoped handle was issued
	KindHandleRevoke  Kind = "handle.revoke"  // a pod's handles were revoked
	KindL4Connect     Kind = "l4.connect"     // a pod reached a datastore through the L4 broker
	KindPodUp         Kind = "pod.up"         // a pod was created
	KindPodTask       Kind = "pod.task"       // an autonomous task pod was created
	KindPodMove       Kind = "pod.move"       // a pod's session was moved to a new shell
	KindPodDown       Kind = "pod.down"       // a pod was torn down
	KindMountRefuse   Kind = "mount.refuse"   // a blocked/credential mount was refused
	KindAutoscaleGrow Kind = "autoscale.grow" // the autoscaler grew a pod
	KindAutoscaleWarn Kind = "autoscale.warn" // the autoscaler warned about a pod
	KindPolicyAllow   Kind = "policy.allow"   // a policy allowed an action
	KindPolicyDeny    Kind = "policy.deny"    // a policy denied an action
)

// Decision is the outcome recorded on an event.
type Decision string

const (
	DecisionAllow   Decision = "allow"
	DecisionRedact  Decision = "redact"
	DecisionBlock   Decision = "block"
	DecisionDeny    Decision = "deny"
	DecisionMonitor Decision = "monitor" // would have been denied, but the policy is in monitor mode — allowed and logged
)

// Event is one audit record. Seq/Time/PrevHash/Hash are assigned by Append; the
// caller fills the rest (through NewEvent, which sanitises it).
type Event struct {
	Seq      int64     `json:"seq"`
	Time     time.Time `json:"time"`
	Source   string    `json:"source,omitempty"` // host id; empty locally, set by the cloud collector
	Pod      string    `json:"pod,omitempty"`
	Identity string    `json:"identity,omitempty"`
	Kind     Kind      `json:"kind"`
	Upstream string    `json:"upstream,omitempty"` // destination host only
	Method   string    `json:"method,omitempty"`
	Path     string    `json:"path,omitempty"` // no query-string
	Status   int       `json:"status,omitempty"`
	Decision Decision  `json:"decision,omitempty"`
	Detail   string    `json:"detail,omitempty"` // never a secret/body
	PrevHash string    `json:"prevHash,omitempty"`
	Hash     string    `json:"hash,omitempty"`
}

// NewEvent sanitises a caller-supplied event so nothing secret-bearing is stored:
// Path loses its query-string (tokens hide there) and Upstream is reduced to its
// host. The Event type has no body/secret field, so there is nothing else to leak.
func NewEvent(e Event) Event {
	if i := strings.IndexByte(e.Path, '?'); i >= 0 {
		e.Path = e.Path[:i]
	}
	e.Upstream = hostOnly(e.Upstream)
	return e
}

// hostOnly reduces a URL or host[:port]/path to its host.
func hostOnly(s string) string {
	if s == "" {
		return ""
	}
	if strings.Contains(s, "://") {
		if u, err := url.Parse(s); err == nil && u.Host != "" {
			return u.Hostname()
		}
	}
	s = strings.SplitN(s, "/", 2)[0] // drop any path
	if h, _, ok := strings.Cut(s, ":"); ok {
		return h
	}
	return s
}

// hashWith returns the chain hash of the event's content plus the previous hash.
func (e Event) hashWith(prev string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%d\x00%d\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%d\x00%s\x00%s\x00%s",
		e.Seq, e.Time.UnixNano(), e.Source, e.Pod, e.Identity, e.Kind, e.Upstream,
		e.Method, e.Path, e.Status, e.Decision, e.Detail, prev)
	return hex.EncodeToString(h.Sum(nil))
}

// Filter selects events for Query.
type Filter struct {
	Pod, Kind, Decision string
	SinceSeq            int64 // only events with seq > SinceSeq
	Limit               int   // 0 = default cap
}

const defaultQueryLimit = 500

// Store is the hash-chained SQLite audit log. Appends are serialised; the daemon
// is the single writer.
type Store struct {
	db  *sql.DB
	now func() time.Time

	mu       sync.Mutex
	nextSeq  int64
	lastHash string
	subs     map[chan Event]struct{}
}

// Open opens (creating if needed) the audit database at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL;`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, err
	}
	s := &Store{db: db, now: time.Now, subs: map[chan Event]struct{}{}}
	if err := s.loadTail(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

const schema = `
CREATE TABLE IF NOT EXISTS events (
  seq       INTEGER PRIMARY KEY,
  ts        INTEGER NOT NULL,
  source    TEXT,
  pod       TEXT, identity TEXT, kind TEXT, upstream TEXT,
  method    TEXT, path TEXT, status INTEGER,
  decision  TEXT, detail TEXT,
  prev_hash TEXT, hash TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_events_pod ON events(pod);
CREATE INDEX IF NOT EXISTS idx_events_kind ON events(kind);
CREATE INDEX IF NOT EXISTS idx_events_decision ON events(decision);
`

// loadTail primes nextSeq + lastHash from the newest row so a reopened store
// continues the same chain.
func (s *Store) loadTail() error {
	var seq int64
	var hash string
	err := s.db.QueryRow(`SELECT seq, hash FROM events ORDER BY seq DESC LIMIT 1`).Scan(&seq, &hash)
	if err == sql.ErrNoRows {
		s.nextSeq, s.lastHash = 1, ""
		return nil
	}
	if err != nil {
		return err
	}
	s.nextSeq, s.lastHash = seq+1, hash
	return nil
}

// Append assigns the event's Seq/Time/PrevHash/Hash, persists it, chains it, and
// fans it out to live subscribers.
func (s *Store) Append(e Event) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e.Seq = s.nextSeq
	e.Time = s.now()
	e.PrevHash = s.lastHash
	e.Hash = e.hashWith(s.lastHash)

	_, err := s.db.Exec(
		`INSERT INTO events(seq,ts,source,pod,identity,kind,upstream,method,path,status,decision,detail,prev_hash,hash)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.Seq, e.Time.UnixNano(), e.Source, e.Pod, e.Identity, string(e.Kind), e.Upstream,
		e.Method, e.Path, e.Status, string(e.Decision), e.Detail, e.PrevHash, e.Hash)
	if err != nil {
		return Event{}, err
	}
	s.nextSeq++
	s.lastHash = e.Hash

	for ch := range s.subs {
		select {
		case ch <- e:
		default: // slow subscriber: drop (Query backfills)
		}
	}
	return e, nil
}

// Query returns matching events, newest first.
func (s *Store) Query(f Filter) ([]Event, error) {
	q := `SELECT seq,ts,source,pod,identity,kind,upstream,method,path,status,decision,detail,prev_hash,hash FROM events WHERE 1=1`
	var args []any
	if f.Pod != "" {
		q += " AND pod = ?"
		args = append(args, f.Pod)
	}
	if f.Kind != "" {
		q += " AND kind = ?"
		args = append(args, f.Kind)
	}
	if f.Decision != "" {
		q += " AND decision = ?"
		args = append(args, f.Decision)
	}
	if f.SinceSeq > 0 {
		q += " AND seq > ?"
		args = append(args, f.SinceSeq)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = defaultQueryLimit
	}
	q += " ORDER BY seq DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvents(rows)
}

// scanEvents reads Event rows (column order matches the SELECT/loadAll queries).
func scanEvents(rows *sql.Rows) ([]Event, error) {
	var out []Event
	for rows.Next() {
		var e Event
		var ts int64
		var kind, decision string
		if err := rows.Scan(&e.Seq, &ts, &e.Source, &e.Pod, &e.Identity, &kind, &e.Upstream,
			&e.Method, &e.Path, &e.Status, &decision, &e.Detail, &e.PrevHash, &e.Hash); err != nil {
			return nil, err
		}
		e.Time = time.Unix(0, ts)
		e.Kind = Kind(kind)
		e.Decision = Decision(decision)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Subscribe returns a channel of newly-appended events and an unsubscribe func.
func (s *Store) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.subs, ch)
			close(ch)
			s.mu.Unlock()
		})
	}
	return ch, cancel
}

// Verify walks the hash chain from the start. It returns ok=false and the seq of
// the first row whose content or link no longer matches its stored hash.
func (s *Store) Verify() (ok bool, brokenAt int64, err error) {
	rows, err := s.db.Query(
		`SELECT seq,ts,source,pod,identity,kind,upstream,method,path,status,decision,detail,prev_hash,hash FROM events ORDER BY seq ASC`)
	if err != nil {
		return false, 0, err
	}
	defer rows.Close()
	events, err := scanEvents(rows)
	if err != nil {
		return false, 0, err
	}
	prev := ""
	for _, e := range events {
		if e.PrevHash != prev || e.hashWith(prev) != e.Hash {
			return false, e.Seq, nil
		}
		prev = e.Hash
	}
	return true, 0, nil
}

// Close releases the database and closes live subscribers.
func (s *Store) Close() error {
	s.mu.Lock()
	for ch := range s.subs {
		delete(s.subs, ch)
		close(ch)
	}
	s.mu.Unlock()
	return s.db.Close()
}
