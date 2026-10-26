// Package store remembers which listings each watch has seen, so a listing is only
// evaluated once per watch and an alert is only sent once.
package store

import (
	"database/sql"
	"time"

	_ "modernc.org/sqlite"
)

// Status values for a listing row.
const (
	StatusSeeded    = "seeded"
	StatusSkipped   = "skipped"
	StatusEvaluated = "evaluated"
	StatusError     = "error"
)

// Row is one listing as seen by one watch.
type Row struct {
	Watch         string
	ItemID        string
	Title         string
	PriceCents    int
	PriceType     string
	URL           string
	Site          string
	Status        string
	Reason        string
	Score         int
	LearningScore int
	EvalJSON      string
	Retries       int
	Notified      bool
	FirstSeen     string
	LastSeen      string
}

// Store is the SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens (and creates) the database at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, stmt := range []string{`PRAGMA journal_mode=WAL`, `PRAGMA busy_timeout=5000`, schema} {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &Store{db: db}, nil
}

const schema = `
CREATE TABLE IF NOT EXISTS listings (
    watch          TEXT NOT NULL,
    item_id        TEXT NOT NULL,
    title          TEXT NOT NULL,
    price_cents    INTEGER NOT NULL,
    price_type     TEXT NOT NULL,
    url            TEXT NOT NULL,
    site           TEXT,
    status         TEXT NOT NULL,
    reason         TEXT,
    score          INTEGER,
    learning_score INTEGER,
    eval_json      TEXT,
    retries        INTEGER NOT NULL DEFAULT 0,
    first_seen     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    notified_at    TIMESTAMP,
    PRIMARY KEY (watch, item_id)
);
CREATE TABLE IF NOT EXISTS feedback (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    watch   TEXT NOT NULL,
    item_id TEXT NOT NULL,
    verdict TEXT NOT NULL,
    at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS lookups (
    query   TEXT PRIMARY KEY,
    results TEXT NOT NULL,
    at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);`

// Lookup returns a cached web lookup younger than maxAge, and whether there was one.
func (s *Store) Lookup(query string, maxAge time.Duration) (string, bool, error) {
	var results string
	err := s.db.QueryRow(`SELECT results FROM lookups WHERE query = ? AND at > ?`,
		query, time.Now().UTC().Add(-maxAge).Format("2006-01-02 15:04:05")).Scan(&results)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return results, err == nil, err
}

// SaveLookup caches the results of a web lookup.
func (s *Store) SaveLookup(query, results string) error {
	_, err := s.db.Exec(`INSERT INTO lookups (query, results) VALUES (?, ?)
		ON CONFLICT(query) DO UPDATE SET results = excluded.results, at = CURRENT_TIMESTAMP`, query, results)
	return err
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

const listingCols = `watch, item_id, title, price_cents, price_type, url, COALESCE(site,''),
		status, COALESCE(reason,''), COALESCE(score,0), COALESCE(learning_score,0), COALESCE(eval_json,''),
		retries, notified_at IS NOT NULL, first_seen, last_seen`

func scanListing(row interface{ Scan(...any) error }) (Row, error) {
	var r Row
	var first, last any
	err := row.Scan(&r.Watch, &r.ItemID, &r.Title, &r.PriceCents, &r.PriceType, &r.URL, &r.Site,
		&r.Status, &r.Reason, &r.Score, &r.LearningScore, &r.EvalJSON, &r.Retries, &r.Notified, &first, &last)
	r.FirstSeen, r.LastSeen = stamp(first), stamp(last)
	return r, err
}

func stamp(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	case time.Time:
		return t.UTC().Format("2006-01-02 15:04")
	default:
		return ""
	}
}

// Get returns a watch's row for an item, or nil when the watch has never seen it.
func (s *Store) Get(watch, itemID string) (*Row, error) {
	row := s.db.QueryRow(`SELECT `+listingCols+` FROM listings WHERE watch = ? AND item_id = ?`, watch, itemID)
	r, err := scanListing(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// Listings returns one watch's listings, alerted ones first, then the most recently
// seen. limit caps the page; 0 means 400.
func (s *Store) Listings(watch string, limit int) ([]Row, error) {
	if limit <= 0 || limit > 500 {
		limit = 400
	}
	rows, err := s.db.Query(`SELECT `+listingCols+` FROM listings WHERE watch = ?
		ORDER BY (notified_at IS NOT NULL) DESC,
			CASE status WHEN 'evaluated' THEN 0 WHEN 'error' THEN 1 WHEN 'skipped' THEN 2 ELSE 3 END,
			last_seen DESC LIMIT ?`, watch, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		r, err := scanListing(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Activity counts a watch's listings by what happened to them.
func (s *Store) Activity(watch string) (map[string]int, error) {
	rows, err := s.db.Query(`SELECT status, COUNT(*), COALESCE(SUM(notified_at IS NOT NULL), 0)
		FROM listings WHERE watch = ? GROUP BY status`, watch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var status string
		var n, alerted int
		if err := rows.Scan(&status, &n, &alerted); err != nil {
			return nil, err
		}
		out[status] = n
		out["total"] += n
		out["alerted"] += alerted
	}
	return out, rows.Err()
}

// Record inserts or updates a listing's outcome for r.Watch.
func (s *Store) Record(r Row) error {
	_, err := s.db.Exec(`
		INSERT INTO listings (watch, item_id, title, price_cents, price_type, url, site, status, reason, score, learning_score, eval_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(watch, item_id) DO UPDATE SET
			title = excluded.title,
			price_cents = excluded.price_cents,
			price_type = excluded.price_type,
			status = excluded.status,
			reason = excluded.reason,
			score = COALESCE(excluded.score, listings.score),
			learning_score = COALESCE(excluded.learning_score, listings.learning_score),
			eval_json = COALESCE(excluded.eval_json, listings.eval_json),
			last_seen = CURRENT_TIMESTAMP`,
		r.Watch, r.ItemID, r.Title, r.PriceCents, r.PriceType, r.URL, r.Site, r.Status,
		nullIfEmpty(r.Reason), zeroAsNil(r.Score), zeroAsNil(r.LearningScore), nullIfEmpty(r.EvalJSON))
	return err
}

// Touch updates last_seen without changing the outcome.
func (s *Store) Touch(watch, itemID string) error {
	_, err := s.db.Exec(`UPDATE listings SET last_seen = CURRENT_TIMESTAMP WHERE watch = ? AND item_id = ?`, watch, itemID)
	return err
}

// MarkNotified records that an alert was sent.
func (s *Store) MarkNotified(watch, itemID string) error {
	_, err := s.db.Exec(`UPDATE listings SET notified_at = CURRENT_TIMESTAMP WHERE watch = ? AND item_id = ?`, watch, itemID)
	return err
}

// AddRetry counts one failed evaluation.
func (s *Store) AddRetry(watch, itemID, reason string) error {
	_, err := s.db.Exec(`UPDATE listings SET retries = retries + 1, status = ?, reason = ?, last_seen = CURRENT_TIMESTAMP
		WHERE watch = ? AND item_id = ?`, StatusError, reason, watch, itemID)
	return err
}

// CountForWatch counts listings recorded for a watch.
func (s *Store) CountForWatch(watch string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM listings WHERE watch = ?`, watch).Scan(&n)
	return n, err
}

// Counts returns the number of listings per status, plus a "notified" total.
func (s *Store) Counts() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT status, COUNT(*) FROM listings GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		out[status] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var notified int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM listings WHERE notified_at IS NOT NULL`).Scan(&notified); err != nil {
		return nil, err
	}
	out["notified"] = notified
	return out, nil
}

// Feedback verdicts from the alert buttons.
const (
	Relevant     = "relevant"
	NotRelevant  = "not_relevant"
	TooExpensive = "too_expensive"
)

// Feedback is one button press on an alert.
type Feedback struct {
	Watch   string
	ItemID  string
	Title   string
	Verdict string
	At      time.Time
}

// AddFeedback stores a verdict, replacing an earlier one for the same alert.
func (s *Store) AddFeedback(watch, itemID, verdict string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM feedback WHERE watch = ? AND item_id = ?`, watch, itemID); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO feedback (watch, item_id, verdict) VALUES (?, ?, ?)`, watch, itemID, verdict); err != nil {
		return err
	}
	return tx.Commit()
}

// FeedbackFor returns a watch's verdicts with the listing titles, newest first.
func (s *Store) FeedbackFor(watch string, limit int) ([]Feedback, error) {
	rows, err := s.db.Query(`SELECT f.watch, f.item_id, COALESCE(l.title,''), f.verdict, f.at
		FROM feedback f LEFT JOIN listings l ON l.watch = f.watch AND l.item_id = f.item_id
		WHERE f.watch = ? ORDER BY f.id DESC LIMIT ?`, watch, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Feedback
	for rows.Next() {
		var f Feedback
		if err := rows.Scan(&f.Watch, &f.ItemID, &f.Title, &f.Verdict, &f.At); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func zeroAsNil(n int) any {
	if n == 0 {
		return nil
	}
	return n
}
