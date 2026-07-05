package auth

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// AttachEmailOrMerge is called once an OTP has been verified for `email` on
// behalf of `currentUserID` (an existing, possibly-anonymous user row).
//
//   - No collision: `email` isn't attached to anyone else — attach it to
//     currentUserID in place. Nothing else moves.
//   - Collision: `email` already belongs to a different user row (e.g. a
//     second device verifying the same address). Fold currentUserID's bills,
//     passkeys, and sessions onto the existing row and delete the
//     now-empty one. This repoints every session — including the one making
//     this very request — so the calling browser stays logged in, now under
//     the canonical row.
//
// Either way, callers should treat the return value as the caller's
// possibly-new identity going forward.
func (m *Manager) AttachEmailOrMerge(ctx context.Context, currentUserID, email string) (finalUserID string, err error) {
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	// Serialize concurrent verifications of the same address.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, email); err != nil {
		return "", err
	}

	var existingID string
	err = tx.QueryRow(ctx, `SELECT id::text FROM users WHERE email = $1`, email).Scan(&existingID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
		// No existing owner for this email — attach it in place.
		if _, err := tx.Exec(ctx, `UPDATE users SET email = $1 WHERE id = $2`, email, currentUserID); err != nil {
			return "", err
		}
		if err := tx.Commit(ctx); err != nil {
			return "", err
		}
		return currentUserID, nil
	}

	if existingID == currentUserID {
		if err := tx.Commit(ctx); err != nil {
			return "", err
		}
		return currentUserID, nil
	}

	// Collision: fold currentUserID into existingID.
	if _, err := tx.Exec(ctx, `UPDATE bill_sessions SET owner_user_id = $1 WHERE owner_user_id = $2`, existingID, currentUserID); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE webauthn_credentials SET user_id = $1 WHERE user_id = $2`, existingID, currentUserID); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE sessions SET user_id = $1 WHERE user_id = $2`, existingID, currentUserID); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, currentUserID); err != nil {
		return "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return existingID, nil
}
