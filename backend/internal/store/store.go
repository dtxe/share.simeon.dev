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
	ID               string
	OwnerUserID      string
	Title            *string
	RestaurantName   *string
	BillDate         *time.Time
	SubtotalCents    int64
	TotalPaidCents   *int64
	ReceiptImagePath *string
	ExtractCount     int
	CreatedAt        time.Time
	UpdatedAt        time.Time
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
	Quantity       float64 `json:"quantity"`
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
		SELECT id::text, title, restaurant_name, bill_date, subtotal_cents,
		       total_paid_cents, receipt_image_path, extract_count, created_at, updated_at
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
		if err := rows.Scan(&b.ID, &b.Title, &b.RestaurantName, &b.BillDate, &b.SubtotalCents,
			&b.TotalPaidCents, &b.ReceiptImagePath, &b.ExtractCount, &b.CreatedAt, &b.UpdatedAt); err != nil {
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
		SELECT id::text, title, restaurant_name, bill_date, subtotal_cents,
		       total_paid_cents, receipt_image_path, extract_count, created_at, updated_at
		FROM bill_sessions
		WHERE id = $1 AND owner_user_id = $2
	`, id, ownerUserID).Scan(&b.ID, &b.Title, &b.RestaurantName, &b.BillDate, &b.SubtotalCents,
		&b.TotalPaidCents, &b.ReceiptImagePath, &b.ExtractCount, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		return nil, noRows(err)
	}
	return &b, nil
}

type SessionPatch struct {
	Title          *string
	RestaurantName *string
	BillDate       *time.Time
	TotalPaidCents *int64
}

// UpdateSession applies whichever fields are non-nil in patch. Bumps
// expires_at back out to 60 days from now on every mutation.
func (s *Store) UpdateSession(ctx context.Context, id, ownerUserID string, patch SessionPatch) error {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE bill_sessions
		SET title = COALESCE($3, title),
		    restaurant_name = COALESCE($4, restaurant_name),
		    bill_date = COALESCE($5, bill_date),
		    total_paid_cents = COALESCE($6, total_paid_cents),
		    updated_at = now(),
		    expires_at = now() + interval '60 days'
		WHERE id = $1 AND owner_user_id = $2
	`, id, ownerUserID, patch.Title, patch.RestaurantName, patch.BillDate, patch.TotalPaidCents)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetReceiptImagePath records where an uploaded receipt landed.
func (s *Store) SetReceiptImagePath(ctx context.Context, id, ownerUserID, path string) error {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE bill_sessions
		SET receipt_image_path = $3, updated_at = now(), expires_at = now() + interval '60 days'
		WHERE id = $1 AND owner_user_id = $2
	`, id, ownerUserID, path)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
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
	Quantity       float64
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
		dish.Quantity = d.Quantity
		dish.Source = source
		dish.SortOrder = i
		err := tx.QueryRow(ctx, `
			INSERT INTO dishes (session_id, name, unit_price_cents, quantity, sort_order, source)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id::text
		`, sessionID, d.Name, d.UnitPriceCents, d.Quantity, i, source).Scan(&dish.ID)
		if err != nil {
			return nil, err
		}
		subtotal += int64(float64(d.UnitPriceCents) * d.Quantity)
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

func (s *Store) UpdateDish(ctx context.Context, dishID, ownerUserID string, name *string, unitPriceCents *int64, quantity *float64) error {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE dishes SET
		  name = COALESCE($3, name),
		  unit_price_cents = COALESCE($4, unit_price_cents),
		  quantity = COALESCE($5, quantity)
		FROM bill_sessions
		WHERE dishes.session_id = bill_sessions.id
		  AND dishes.id = $1 AND bill_sessions.owner_user_id = $2
	`, dishID, ownerUserID, name, unitPriceCents, quantity)
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

// GenerateShareToken (re)creates the bill's public view token, returning the
// raw token to hand to the client — it's never retrievable again, only its
// hash is stored.
func (s *Store) GenerateShareToken(ctx context.Context, sessionID, ownerUserID string) (string, error) {
	b := make([]byte, 16) // 128 bits — a public read-only capability token
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	raw := "bv_" + base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(raw))

	tag, err := s.Pool.Exec(ctx, `
		UPDATE bill_sessions SET view_token_hash = $3, updated_at = now()
		WHERE id = $1 AND owner_user_id = $2
	`, sessionID, ownerUserID, sum[:])
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
		SELECT id::text, title, restaurant_name, bill_date, subtotal_cents,
		       total_paid_cents, receipt_image_path, extract_count, created_at, updated_at
		FROM bill_sessions
		WHERE view_token_hash = $1
	`, sum[:]).Scan(&b.ID, &b.Title, &b.RestaurantName, &b.BillDate, &b.SubtotalCents,
		&b.TotalPaidCents, &b.ReceiptImagePath, &b.ExtractCount, &b.CreatedAt, &b.UpdatedAt)
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

// DeleteExpiredBillSessions removes stale bills (cascading to their people,
// dishes, portions, and extraction_runs) and returns the receipt image
// paths that need deleting from disk too — the DB delete doesn't touch the
// filesystem, so the caller (internal/cleanup) is responsible for that.
func (s *Store) DeleteExpiredBillSessions(ctx context.Context) (receiptPaths []string, count int64, err error) {
	rows, err := s.Pool.Query(ctx, `
		DELETE FROM bill_sessions WHERE expires_at < now()
		RETURNING receipt_image_path
	`)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	receiptPaths = []string{}
	for rows.Next() {
		var p *string
		if err := rows.Scan(&p); err != nil {
			return nil, 0, err
		}
		count++
		if p != nil {
			receiptPaths = append(receiptPaths, *p)
		}
	}
	return receiptPaths, count, rows.Err()
}
