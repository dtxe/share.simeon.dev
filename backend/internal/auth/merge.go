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
//     second device verifying the same address). Fold currentUserID's bills
//     and passkeys onto the existing row and delete the now-empty one.
//     Sessions are deliberately NOT repointed — currentUserID's sessions
//     (including the pre-auth one making this request) cascade-delete with
//     the row, so a fixed/stolen anonymous token can't ride along as an
//     authenticated session. Callers must issue a fresh session for the
//     returned identity.
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

	// Collision: fold currentUserID into existingID. Sessions are not moved
	// — DELETE FROM users cascades and kills currentUserID's session(s), so
	// the pre-auth token is dead once this returns.
	if _, err := tx.Exec(ctx, `UPDATE bill_sessions SET owner_user_id = $1 WHERE owner_user_id = $2`, existingID, currentUserID); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE webauthn_credentials SET user_id = $1 WHERE user_id = $2`, existingID, currentUserID); err != nil {
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

// MergeAnonymousInto folds a throwaway anonymous identity into a durable
// target user, returning false without side effects if the source has already
// been upgraded with email or passkeys. Passkey login uses this for the common
// returning-user case: a fresh browser auto-provisions an anonymous row, may
// create local bills, then proves ownership of an existing passkey account.
func (m *Manager) MergeAnonymousInto(ctx context.Context, sourceUserID, targetUserID string) (bool, error) {
	if sourceUserID == targetUserID {
		return false, nil
	}

	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	var sourceIsAnonymous bool
	if err := tx.QueryRow(ctx, `
		SELECT email IS NULL
		AND NOT EXISTS (SELECT 1 FROM webauthn_credentials WHERE user_id = users.id)
		FROM users
		WHERE id = $1
	`, sourceUserID).Scan(&sourceIsAnonymous); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if !sourceIsAnonymous {
		return false, tx.Commit(ctx)
	}

	if _, err := tx.Exec(ctx, `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, targetUserID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE bill_sessions SET owner_user_id = $1 WHERE owner_user_id = $2`, targetUserID, sourceUserID); err != nil {
		return false, err
	}
	// Sessions are not moved — DELETE FROM users cascades and kills
	// sourceUserID's session(s), so the pre-auth anonymous token dies here
	// instead of surviving as a session on the target account.
	if _, err := tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, sourceUserID); err != nil {
		return false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}
