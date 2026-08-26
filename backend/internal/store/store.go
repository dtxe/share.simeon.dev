// Package store is the repository layer over Postgres for bills, people,
// dishes, and portions. Every query that touches an existing bill session
// is scoped by owner_user_id in the WHERE clause itself (not a separate
// check-then-act step), so "not my bill" and "doesn't exist" are
// indistinguishable — both come back as ErrNotFound, which httpapi turns
// into a 404.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"share/backend/internal/split"
)

var ErrNotFound = errors.New("store: not found")

type Store struct {
	Pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{Pool: pool}
}

type BillSession struct {
	ID                     string
	OwnerUserID            string
	Title                  *string
	Notes                  *string
	RestaurantName         *string
	BillDate               *time.Time
	SubtotalCents          int64
	TotalPaidCents         *int64
	ReceiptImagePath       *string
	ReceiptImageCompressed bool
	ExtractCount           int
	CreatedAt              time.Time
	UpdatedAt              time.Time
	ShareToken             *string
	ShareLinkExists        bool
}

type Person struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
	SortOrder int    `json:"sortOrder"`
}

type Dish struct {
	ID             string  `json:"id"`
	SessionID      string  `json:"sessionId"`
	Name           string  `json:"name"`
	UnitPriceCents int64   `json:"unitPriceCents"`
	Quantity       float64 `json:"-"`
	SortOrder      int     `json:"sortOrder"`
	Source         string  `json:"source"`
}

type Portion struct {
	DishID   string  `json:"dishId"`
	PersonID string  `json:"personId"`
	Shares   float64 `json:"shares"`
}

func noRows(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// CreateSession makes a new, empty bill owned by ownerUserID.
func (s *Store) CreateSession(ctx context.Context, ownerUserID string) (*BillSession, error) {
	var b BillSession
	b.OwnerUserID = ownerUserID
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO bill_sessions (owner_user_id)
		VALUES ($1)
		RETURNING id::text, subtotal_cents, extract_count, created_at, updated_at
	`, ownerUserID).Scan(&b.ID, &b.SubtotalCents, &b.ExtractCount, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// ListSessionsByOwner is the "history" list — most recently updated first.
func (s *Store) ListSessionsByOwner(ctx context.Context, ownerUserID string) ([]BillSession, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id::text, title, notes, restaurant_name, bill_date, subtotal_cents,
		       total_paid_cents, receipt_image_path, receipt_image_compressed, extract_count, created_at, updated_at, share_token, view_token_hash IS NOT NULL
		FROM bill_sessions
		WHERE owner_user_id = $1
		ORDER BY updated_at DESC
	`, ownerUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []BillSession{}
	for rows.Next() {
		var b BillSession
		b.OwnerUserID = ownerUserID
		if err := rows.Scan(&b.ID, &b.Title, &b.Notes, &b.RestaurantName, &b.BillDate, &b.SubtotalCents,
			&b.TotalPaidCents, &b.ReceiptImagePath, &b.ReceiptImageCompressed, &b.ExtractCount, &b.CreatedAt, &b.UpdatedAt, &b.ShareToken, &b.ShareLinkExists); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// GetSession fetches a bill, scoped to its owner.
func (s *Store) GetSession(ctx context.Context, id, ownerUserID string) (*BillSession, error) {
	var b BillSession
	b.OwnerUserID = ownerUserID
	err := s.Pool.QueryRow(ctx, `
		SELECT id::text, title, notes, restaurant_name, bill_date, subtotal_cents,
		       total_paid_cents, receipt_image_path, receipt_image_compressed, extract_count, created_at, updated_at, share_token, view_token_hash IS NOT NULL
		FROM bill_sessions
		WHERE id = $1 AND owner_user_id = $2
	`, id, ownerUserID).Scan(&b.ID, &b.Title, &b.Notes, &b.RestaurantName, &b.BillDate, &b.SubtotalCents,
		&b.TotalPaidCents, &b.ReceiptImagePath, &b.ReceiptImageCompressed, &b.ExtractCount, &b.CreatedAt, &b.UpdatedAt, &b.ShareToken, &b.ShareLinkExists)
	if err != nil {
		return nil, noRows(err)
	}
	return &b, nil
}

type SessionPatch struct {
	Title          *string
	Notes          *string
	RestaurantName *string
	BillDate       *time.Time
	TotalPaidCents *int64
}

// UpdateSession applies whichever fields are non-nil in patch. Bumps
// expires_at back out to 60 days from now on every mutation.
func (s *Store) UpdateSession(ctx context.Context, id, ownerUserID string, patch SessionPatch) error {
	if patch.Notes != nil {
		notes := strings.TrimSpace(*patch.Notes)
		patch.Notes = &notes
	}
	tag, err := s.Pool.Exec(ctx, `
		UPDATE bill_sessions
		SET title = COALESCE($3, title),
		    notes = CASE WHEN $4::text IS NULL THEN notes WHEN $4::text = '' THEN NULL ELSE $4::text END,
		    restaurant_name = COALESCE($5, restaurant_name),
		    bill_date = COALESCE($6, bill_date),
		    total_paid_cents = COALESCE($7, total_paid_cents),
		    updated_at = now(),
		    expires_at = now() + interval '60 days'
		WHERE id = $1 AND owner_user_id = $2
	`, id, ownerUserID, patch.Title, patch.Notes, patch.RestaurantName, patch.BillDate, patch.TotalPaidCents)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetReceiptImagePath records where an uploaded receipt landed. A new upload
// always resets the compressed flag so the next successful extraction can
// recompress from the fresh original.
func (s *Store) SetReceiptImagePath(ctx context.Context, id, ownerUserID, path string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var old *string
	err = tx.QueryRow(ctx, `SELECT receipt_image_path FROM bill_sessions WHERE id = $1 AND owner_user_id = $2 FOR UPDATE`, id, ownerUserID).Scan(&old)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE bill_sessions
		SET receipt_image_path = $3,
		    receipt_image_compressed = false,
		    updated_at = now(),
		    expires_at = now() + interval '60 days'
		WHERE id = $1 AND owner_user_id = $2
	`, id, ownerUserID, path)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if old != nil {
		if _, err = tx.Exec(ctx, `INSERT INTO receipt_deletion_queue (path) VALUES ($1) ON CONFLICT (path) DO NOTHING`, *old); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ReplaceCompressedReceipt atomically swaps a receipt only if the captured
// owner and source path are still current, and queues the source for deletion.
func (s *Store) ReplaceCompressedReceipt(ctx context.Context, id, ownerUserID, oldPath, newPath string) (bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		UPDATE bill_sessions
		SET receipt_image_path = $4, receipt_image_compressed = true, updated_at = now()
		WHERE id = $1 AND owner_user_id = $2 AND receipt_image_path = $3 AND receipt_image_compressed = false
	`, id, ownerUserID, oldPath, newPath)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if _, err = tx.Exec(ctx, `INSERT INTO receipt_deletion_queue (path) VALUES ($1) ON CONFLICT (path) DO NOTHING`, oldPath); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// IncrementExtractCount is race-safe: it only increments (and reports
// success) if the session is still under the cap, so concurrent requests
// can't blow past it.
func (s *Store) IncrementExtractCount(ctx context.Context, id, ownerUserID string, max int) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE bill_sessions
		SET extract_count = extract_count + 1, updated_at = now()
		WHERE id = $1 AND owner_user_id = $2 AND extract_count < $3
	`, id, ownerUserID, max)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// AddPerson appends a person to a bill the caller owns.
func (s *Store) AddPerson(ctx context.Context, sessionID, ownerUserID, name string) (*Person, error) {
	if _, err := s.GetSession(ctx, sessionID, ownerUserID); err != nil {
		return nil, err
	}
	var p Person
	p.SessionID = sessionID
	p.Name = name
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO people (session_id, name, sort_order)
		VALUES ($1, $2, (SELECT COALESCE(MAX(sort_order), -1) + 1 FROM people WHERE session_id = $1))
		RETURNING id::text, sort_order
	`, sessionID, name).Scan(&p.ID, &p.SortOrder)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// DeletePerson removes a person from a bill the caller owns (cascades their portions).
func (s *Store) DeletePerson(ctx context.Context, personID, ownerUserID string) error {
	tag, err := s.Pool.Exec(ctx, `
		DELETE FROM people
		USING bill_sessions
		WHERE people.session_id = bill_sessions.id
		  AND people.id = $1
		  AND bill_sessions.owner_user_id = $2
	`, personID, ownerUserID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RenamePerson(ctx context.Context, personID, ownerUserID, name string) error {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE people SET name = $3
		FROM bill_sessions
		WHERE people.session_id = bill_sessions.id
		  AND people.id = $1
		  AND bill_sessions.owner_user_id = $2
	`, personID, ownerUserID, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListPeople(ctx context.Context, sessionID, ownerUserID string) ([]Person, error) {
	if _, err := s.GetSession(ctx, sessionID, ownerUserID); err != nil {
		return nil, err
	}
	return s.listPeopleUnchecked(ctx, sessionID)
}

// ListPeoplePublic is for the public share view: the caller has already
// proven authorization by presenting a valid view token (see
// GetByViewToken), so there's no separate owner to check here.
func (s *Store) ListPeoplePublic(ctx context.Context, sessionID string) ([]Person, error) {
	return s.listPeopleUnchecked(ctx, sessionID)
}

// ListDishesPublic mirrors ListPeoplePublic — authorization already proven
// by a valid view token, no owner check needed.
func (s *Store) ListDishesPublic(ctx context.Context, sessionID string) ([]Dish, error) {
	return s.listDishesUnchecked(ctx, sessionID)
}

// ListPortionsPublic mirrors ListPeoplePublic — authorization already proven
// by a valid view token, no owner check needed.
func (s *Store) ListPortionsPublic(ctx context.Context, sessionID string) ([]Portion, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT portions.dish_id::text, portions.person_id::text, portions.shares
		FROM portions
		JOIN dishes ON dishes.id = portions.dish_id
		WHERE dishes.session_id = $1
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Portion{}
	for rows.Next() {
		var p Portion
		if err := rows.Scan(&p.DishID, &p.PersonID, &p.Shares); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) listPeopleUnchecked(ctx context.Context, sessionID string) ([]Person, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id::text, name, sort_order FROM people
		WHERE session_id = $1 ORDER BY sort_order
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Person{}
	for rows.Next() {
		var p Person
		p.SessionID = sessionID
		if err := rows.Scan(&p.ID, &p.Name, &p.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type NewDish struct {
	Name           string
	UnitPriceCents int64
	Source         string // "manual" | "llm_extracted"
}

// ReplaceDishes deletes all existing dishes (and their portions, via
// cascade) and inserts the given list — used both for manual entry and for
// accepting a reviewed/edited LLM extraction. Recomputes subtotal_cents.
func (s *Store) ReplaceDishes(ctx context.Context, sessionID, ownerUserID string, dishes []NewDish) ([]Dish, error) {
	if _, err := s.GetSession(ctx, sessionID, ownerUserID); err != nil {
		return nil, err
	}

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM dishes WHERE session_id = $1`, sessionID); err != nil {
		return nil, err
	}

	out := make([]Dish, 0, len(dishes))
	var subtotal int64
	for i, d := range dishes {
		source := d.Source
		if source == "" {
			source = "manual"
		}
		var dish Dish
		dish.SessionID = sessionID
		dish.Name = d.Name
		dish.UnitPriceCents = d.UnitPriceCents
		dish.Quantity = 1
		dish.Source = source
		dish.SortOrder = i
		err := tx.QueryRow(ctx, `
			INSERT INTO dishes (session_id, name, unit_price_cents, quantity, sort_order, source)
			VALUES ($1, $2, $3, 1, $4, $5)
			RETURNING id::text
		`, sessionID, d.Name, d.UnitPriceCents, i, source).Scan(&dish.ID)
		if err != nil {
			return nil, err
		}
		subtotal += d.UnitPriceCents
		out = append(out, dish)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE bill_sessions SET subtotal_cents = $2, updated_at = now() WHERE id = $1
	`, sessionID, subtotal); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) ListDishes(ctx context.Context, sessionID, ownerUserID string) ([]Dish, error) {
	if _, err := s.GetSession(ctx, sessionID, ownerUserID); err != nil {
		return nil, err
	}
	return s.listDishesUnchecked(ctx, sessionID)
}

func (s *Store) listDishesUnchecked(ctx context.Context, sessionID string) ([]Dish, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id::text, name, unit_price_cents, quantity, sort_order, source
		FROM dishes WHERE session_id = $1 ORDER BY sort_order
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Dish{}
	for rows.Next() {
		var d Dish
		d.SessionID = sessionID
		if err := rows.Scan(&d.ID, &d.Name, &d.UnitPriceCents, &d.Quantity, &d.SortOrder, &d.Source); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// AddDish appends a single dish to a bill the caller owns, without touching
// any existing dish or its portions — unlike ReplaceDishes, which is
// reserved for the extraction bulk-replace path.
func (s *Store) AddDish(ctx context.Context, sessionID, ownerUserID string, d NewDish) (*Dish, error) {
	if _, err := s.GetSession(ctx, sessionID, ownerUserID); err != nil {
		return nil, err
	}
	source := d.Source
	if source == "" {
		source = "manual"
	}
	var dish Dish
	dish.SessionID = sessionID
	dish.Name = d.Name
	dish.UnitPriceCents = d.UnitPriceCents
	dish.Quantity = 1
	dish.Source = source
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO dishes (session_id, name, unit_price_cents, quantity, sort_order, source)
		VALUES ($1, $2, $3, 1, (SELECT COALESCE(MAX(sort_order), -1) + 1 FROM dishes WHERE session_id = $1), $4)
		RETURNING id::text, sort_order
	`, sessionID, d.Name, d.UnitPriceCents, source).Scan(&dish.ID, &dish.SortOrder)
	if err != nil {
		return nil, err
	}
	if err := s.recalculateSubtotalForDish(ctx, dish.ID); err != nil {
		return nil, err
	}
	return &dish, nil
}

// UpsertPortion sets (dishID, personID)'s share count. Scoped to a dish that
// belongs to a session the caller owns, AND to a person in that same
// session — otherwise a known person UUID from a different user's bill
// could be cross-linked in via IDOR.
func (s *Store) UpsertPortion(ctx context.Context, dishID, personID, ownerUserID string, shares float64) error {
	tag, err := s.Pool.Exec(ctx, `
		INSERT INTO portions (dish_id, person_id, shares)
		SELECT $1, $2, $4
		FROM dishes
		JOIN bill_sessions ON bill_sessions.id = dishes.session_id
		JOIN people ON people.id = $2 AND people.session_id = dishes.session_id
		WHERE dishes.id = $1 AND bill_sessions.owner_user_id = $3
		ON CONFLICT (dish_id, person_id) DO UPDATE SET shares = $4, updated_at = now()
	`, dishID, personID, ownerUserID, shares)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UpdateDish(ctx context.Context, dishID, ownerUserID string, name *string, unitPriceCents *int64) error {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE dishes SET
		  name = COALESCE($3, name),
		  unit_price_cents = COALESCE($4, unit_price_cents)
		FROM bill_sessions
		WHERE dishes.session_id = bill_sessions.id
		  AND dishes.id = $1 AND bill_sessions.owner_user_id = $2
	`, dishID, ownerUserID, name, unitPriceCents)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return s.recalculateSubtotalForDish(ctx, dishID)
}

func (s *Store) DeleteDish(ctx context.Context, dishID, ownerUserID string) error {
	tag, err := s.Pool.Exec(ctx, `
		DELETE FROM dishes
		USING bill_sessions
		WHERE dishes.session_id = bill_sessions.id
		  AND dishes.id = $1 AND bill_sessions.owner_user_id = $2
	`, dishID, ownerUserID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) recalculateSubtotalForDish(ctx context.Context, dishID string) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE bill_sessions SET subtotal_cents = sub.total, updated_at = now()
		FROM (
			SELECT session_id, COALESCE(SUM(ROUND(unit_price_cents * quantity)), 0)::bigint AS total
			FROM dishes WHERE session_id = (SELECT session_id FROM dishes WHERE id = $1)
			GROUP BY session_id
		) sub
		WHERE bill_sessions.id = sub.session_id
	`, dishID)
	return err
}

type ShareLink struct {
	Token     *string
	Exists    bool
	Available bool
}

func shareLinkForStoredToken(token *string, hash []byte) (ShareLink, bool) {
	if token != nil {
		return ShareLink{Token: token, Exists: true, Available: true}, true
	}
	if hash != nil {
		return ShareLink{Exists: true, Available: false}, true
	}
	return ShareLink{}, false
}

func newShareToken() (string, []byte, error) {
	b := make([]byte, 16) // 128 bits — a public read-only capability token
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	raw := "bv_" + base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(raw))
	return raw, sum[:], nil
}

// GetOrCreateShareToken reuses an existing plaintext token. Legacy rows with
// only view_token_hash remain active but cannot be displayed or auto-rotated.
func (s *Store) GetOrCreateShareToken(ctx context.Context, sessionID, ownerUserID string) (ShareLink, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return ShareLink{}, err
	}
	defer tx.Rollback(ctx)
	var token *string
	var hash []byte
	err = tx.QueryRow(ctx, `SELECT share_token, view_token_hash FROM bill_sessions WHERE id = $1 AND owner_user_id = $2 FOR UPDATE`, sessionID, ownerUserID).Scan(&token, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return ShareLink{}, ErrNotFound
	}
	if err != nil {
		return ShareLink{}, err
	}
	if link, exists := shareLinkForStoredToken(token, hash); exists {
		return link, tx.Commit(ctx)
	}
	raw, sum, err := newShareToken()
	if err != nil {
		return ShareLink{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE bill_sessions SET share_token = $3, view_token_hash = $4, updated_at = now() WHERE id = $1 AND owner_user_id = $2`, sessionID, ownerUserID, raw, sum); err != nil {
		return ShareLink{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ShareLink{}, err
	}
	return ShareLink{Token: &raw, Exists: true, Available: true}, nil
}

// RotateShareToken explicitly replaces both the plaintext token and lookup
// hash, invalidating every URL made with the previous token.
func (s *Store) RotateShareToken(ctx context.Context, sessionID, ownerUserID string) (string, error) {
	raw, sum, err := newShareToken()
	if err != nil {
		return "", err
	}

	tag, err := s.Pool.Exec(ctx, `
		UPDATE bill_sessions SET share_token = $3, view_token_hash = $4, updated_at = now()
		WHERE id = $1 AND owner_user_id = $2
	`, sessionID, ownerUserID, raw, sum)
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		return "", ErrNotFound
	}
	return raw, nil
}

// GetByViewToken is the public, read-only lookup — no owner check, because
// the token itself is the credential.
func (s *Store) GetByViewToken(ctx context.Context, rawToken string) (*BillSession, error) {
	sum := sha256.Sum256([]byte(rawToken))
	var b BillSession
	err := s.Pool.QueryRow(ctx, `
		SELECT id::text, title, notes, restaurant_name, bill_date, subtotal_cents,
		       total_paid_cents, receipt_image_path, receipt_image_compressed, extract_count, created_at, updated_at
		FROM bill_sessions
		WHERE view_token_hash = $1
	`, sum[:]).Scan(&b.ID, &b.Title, &b.Notes, &b.RestaurantName, &b.BillDate, &b.SubtotalCents,
		&b.TotalPaidCents, &b.ReceiptImagePath, &b.ReceiptImageCompressed, &b.ExtractCount, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		return nil, noRows(err)
	}
	return &b, nil
}

// GetBreakdown assembles a session's dishes/portions/people and runs the
// split calculation — the one place httpapi needs to call for both the
// owner-facing breakdown endpoint and the public share view.
func (s *Store) GetBreakdown(ctx context.Context, sessionID, ownerUserID string) (*BillSession, split.Result, error) {
	sess, err := s.GetSession(ctx, sessionID, ownerUserID)
	if err != nil {
		return nil, split.Result{}, err
	}
	return s.computeBreakdown(ctx, sess)
}

// GetBreakdownByViewToken is the public-view equivalent, with no owner check.
func (s *Store) GetBreakdownByViewToken(ctx context.Context, rawToken string) (*BillSession, split.Result, error) {
	sess, err := s.GetByViewToken(ctx, rawToken)
	if err != nil {
		return nil, split.Result{}, err
	}
	return s.computeBreakdown(ctx, sess)
}

func (s *Store) computeBreakdown(ctx context.Context, sess *BillSession) (*BillSession, split.Result, error) {
	dishRows, err := s.Pool.Query(ctx, `SELECT id::text, unit_price_cents, quantity FROM dishes WHERE session_id = $1`, sess.ID)
	if err != nil {
		return nil, split.Result{}, err
	}
	var dishes []split.Dish
	for dishRows.Next() {
		var d split.Dish
		if err := dishRows.Scan(&d.ID, &d.UnitPriceCents, &d.Quantity); err != nil {
			dishRows.Close()
			return nil, split.Result{}, err
		}
		dishes = append(dishes, d)
	}
	dishRows.Close()
	if err := dishRows.Err(); err != nil {
		return nil, split.Result{}, err
	}

	peopleRows, err := s.Pool.Query(ctx, `SELECT id::text FROM people WHERE session_id = $1 ORDER BY sort_order`, sess.ID)
	if err != nil {
		return nil, split.Result{}, err
	}
	var peopleIDs []string
	for peopleRows.Next() {
		var id string
		if err := peopleRows.Scan(&id); err != nil {
			peopleRows.Close()
			return nil, split.Result{}, err
		}
		peopleIDs = append(peopleIDs, id)
	}
	peopleRows.Close()
	if err := peopleRows.Err(); err != nil {
		return nil, split.Result{}, err
	}

	portionRows, err := s.Pool.Query(ctx, `
		SELECT portions.dish_id::text, portions.person_id::text, portions.shares
		FROM portions JOIN dishes ON dishes.id = portions.dish_id
		WHERE dishes.session_id = $1
	`, sess.ID)
	if err != nil {
		return nil, split.Result{}, err
	}
	var portions []split.Portion
	for portionRows.Next() {
		var p split.Portion
		if err := portionRows.Scan(&p.DishID, &p.PersonID, &p.Shares); err != nil {
			portionRows.Close()
			return nil, split.Result{}, err
		}
		portions = append(portions, p)
	}
	portionRows.Close()
	if err := portionRows.Err(); err != nil {
		return nil, split.Result{}, err
	}

	var totalPaid int64
	if sess.TotalPaidCents != nil {
		totalPaid = *sess.TotalPaidCents
	}

	return sess, split.Compute(dishes, portions, peopleIDs, totalPaid), nil
}

func (s *Store) ListPortions(ctx context.Context, sessionID, ownerUserID string) ([]Portion, error) {
	if _, err := s.GetSession(ctx, sessionID, ownerUserID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT portions.dish_id::text, portions.person_id::text, portions.shares
		FROM portions
		JOIN dishes ON dishes.id = portions.dish_id
		WHERE dishes.session_id = $1
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Portion{}
	for rows.Next() {
		var p Portion
		if err := rows.Scan(&p.DishID, &p.PersonID, &p.Shares); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// --- Cleanup: used only by internal/cleanup's periodic sweep. ---

func (s *Store) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (s *Store) DeleteExpiredOTPCodes(ctx context.Context) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM otp_codes WHERE expires_at < now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (s *Store) DeleteExpiredWebauthnCeremonies(ctx context.Context) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM webauthn_ceremonies WHERE expires_at < now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// DeleteExpiredBillSessions removes stale bills and durably queues their
// receipt paths in the same transaction.
func (s *Store) DeleteExpiredBillSessions(ctx context.Context) (int64, error) {
	var count int64
	err := s.Pool.QueryRow(ctx, `WITH deleted AS (
		DELETE FROM bill_sessions
		WHERE expires_at < now()
		RETURNING receipt_image_path
	), queued AS (
		INSERT INTO receipt_deletion_queue (path)
		SELECT receipt_image_path FROM deleted WHERE receipt_image_path IS NOT NULL
		ON CONFLICT (path) DO NOTHING
	)
	SELECT count(*) FROM deleted`).Scan(&count)
	return count, err
}

type ReceiptDeletion struct {
	ID   int64
	Path string
}

func (s *Store) EnqueueReceiptDeletion(ctx context.Context, path string) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO receipt_deletion_queue (path) VALUES ($1) ON CONFLICT (path) DO NOTHING`, path)
	return err
}

func (s *Store) ClaimReceiptDeletions(ctx context.Context, limit int) ([]ReceiptDeletion, error) {
	if limit <= 0 {
		return []ReceiptDeletion{}, nil
	}
	rows, err := s.Pool.Query(ctx, `WITH candidates AS (
		SELECT id FROM receipt_deletion_queue
		WHERE next_attempt_at <= now() AND (processing_until IS NULL OR processing_until < now())
		ORDER BY id FOR UPDATE SKIP LOCKED LIMIT $1
	)
	UPDATE receipt_deletion_queue q
	SET processing_until = now() + interval '10 minutes'
	FROM candidates c WHERE q.id = c.id
		RETURNING q.id, q.path`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReceiptDeletion{}
	for rows.Next() {
		var d ReceiptDeletion
		if err := rows.Scan(&d.ID, &d.Path); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) AckReceiptDeletion(ctx context.Context, id int64) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM receipt_deletion_queue WHERE id = $1`, id)
	return err
}
func (s *Store) RetryReceiptDeletion(ctx context.Context, id int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE receipt_deletion_queue SET attempts = attempts + 1, processing_until = NULL, next_attempt_at = now() + LEAST(interval '24 hours', interval '1 minute' * power(2::numeric, LEAST(attempts + 1, 10))) WHERE id = $1`, id)
	return err
}

// --- Normalized extraction telemetry ---

// BeginExtractionRunInput contains parameters known upfront, before the
// extraction strategy begins making LLM calls.
type BeginExtractionRunInput struct {
	SessionID       string
	Strategy        string
	MaxCalls        int
	ReceiptCapCents int
	ReservedCents   int
}

// BeginExtractionRun inserts a new extraction_runs row with status 'running'
// and returns the run UUID string.
func (s *Store) BeginExtractionRun(ctx context.Context, in BeginExtractionRunInput) (string, error) {
	var runID string
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO extraction_runs (session_id, strategy, status, max_calls, receipt_cap_cents, reserved_cents, attempt_count)
		VALUES ($1, $2, 'running', $3, $4, $5, 0)
		RETURNING id::text
	`, in.SessionID, in.Strategy, in.MaxCalls, in.ReceiptCapCents, in.ReservedCents).Scan(&runID)
	if err != nil {
		return "", err
	}
	return runID, nil
}

// ExtractionAttempt represents one LLM call within an extraction run.
type ExtractionAttempt struct {
	Attempt           int
	Provider          string
	Model             string
	Status            string // "success" | "error"
	ErrorMessage      *string
	RawResponse       []byte
	PromptTokens      *int
	CompletionTokens  *int
	CostCents         *int
	SubtotalMatched   *bool
	SubtotalDiffCents *int64
}

// CompleteExtractionRunInput contains the terminal state of an extraction run
// and all child attempts to persist atomically.
type CompleteExtractionRunInput struct {
	RunID                string
	Status               string // "success" | "error" | "rejected"
	ErrorMessage         *string
	SubtotalMatched      *bool
	SubtotalDiffCents    *int64
	KnownActualCostCents *int
	AccountedCostCents   *int
	ReservationAccepted  bool
	SpendReconciled      bool
	CompletedAt          time.Time
	Attempts             []ExtractionAttempt
}

// CompleteExtractionRun transactionally inserts all child extraction_attempts
// and updates the parent run's terminal fields (status, costs, completion
// time, etc.) in a single transaction.
func (s *Store) CompleteExtractionRun(ctx context.Context, in CompleteExtractionRunInput) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	for _, a := range in.Attempts {
		var rawResponse any
		if len(a.RawResponse) > 0 {
			// raw_response is BYTEA, not JSONB: JSONB would normalize the body
			// and lose the exact upstream bytes we are troubleshooting.
			rawResponse = a.RawResponse
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO extraction_attempts (run_id, attempt, provider, model, status, error_message, raw_response,
				prompt_tokens, completion_tokens, cost_cents, subtotal_matched, subtotal_diff_cents)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			ON CONFLICT (run_id, attempt) DO UPDATE SET
				provider = EXCLUDED.provider,
				model = EXCLUDED.model,
				status = EXCLUDED.status,
				error_message = EXCLUDED.error_message,
				raw_response = EXCLUDED.raw_response,
				prompt_tokens = EXCLUDED.prompt_tokens,
				completion_tokens = EXCLUDED.completion_tokens,
				cost_cents = EXCLUDED.cost_cents,
				subtotal_matched = EXCLUDED.subtotal_matched,
				subtotal_diff_cents = EXCLUDED.subtotal_diff_cents
		`, in.RunID, a.Attempt, a.Provider, a.Model, a.Status, a.ErrorMessage, rawResponse,
			a.PromptTokens, a.CompletionTokens, a.CostCents, a.SubtotalMatched, a.SubtotalDiffCents); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE extraction_runs
		SET status = $2,
		    error_message = $3,
		    subtotal_matched = $4,
		    subtotal_diff_cents = $5,
		    known_actual_cost_cents = $6,
		    accounted_cost_cents = $7,
		    attempt_count = $8,
		    completed_at = $9,
		    spend_reconciled = $10,
		    reservation_accepted = $11
		WHERE id = $1
	`, in.RunID, in.Status, in.ErrorMessage, in.SubtotalMatched, in.SubtotalDiffCents,
		in.KnownActualCostCents, in.AccountedCostCents, len(in.Attempts), in.CompletedAt,
		in.SpendReconciled, in.ReservationAccepted); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// MarkExtractionSpendReconciled sets spend_reconciled = true on the run,
// indicating its actual cost has been incorporated into the daily spend
// ledger.
func (s *Store) MarkExtractionSpendReconciled(ctx context.Context, runID string) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE extraction_runs SET spend_reconciled = true WHERE id = $1
	`, runID)
	return err
}
