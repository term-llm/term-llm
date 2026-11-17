package session

import (
	"context"
	"time"
)

// UpdateContextEstimateWithWindow persists the observed context size and its limit together.
// Stores without window support still receive the size and message count.
func UpdateContextEstimateWithWindow(ctx context.Context, store Store, id string, lastTotalTokens, lastMessageCount, window int) error {
	if s, ok := store.(interface {
		UpdateContextEstimateWithWindow(context.Context, string, int, int, int) error
	}); ok {
		return s.UpdateContextEstimateWithWindow(ctx, id, lastTotalTokens, lastMessageCount, window)
	}
	return store.UpdateContextEstimate(ctx, id, lastTotalTokens, lastMessageCount)
}

// PersistContextEstimate saves one engine snapshot and keeps the live session
// metadata in sync with the successful write.
func PersistContextEstimate(ctx context.Context, store Store, sess *Session, total, count, window int) error {
	if err := UpdateContextEstimateWithWindow(ctx, store, sess.ID, total, count, window); err != nil {
		return err
	}
	sess.LastTotalTokens, sess.LastMessageCount, sess.LastContextWindow = total, count, window
	return nil
}

func (s *LoggingStore) UpdateContextEstimateWithWindow(ctx context.Context, id string, lastTotalTokens, lastMessageCount, window int) error {
	err := UpdateContextEstimateWithWindow(ctx, s.Store, id, lastTotalTokens, lastMessageCount, window)
	s.logOnce("UpdateContextEstimateWithWindow", err)
	return err
}

func (s *SQLiteStore) contextWindowColumn() string {
	if s.hasLastContextWindow {
		return "last_context_window"
	}
	return "0 AS last_context_window"
}

// UpdateContextEstimate replaces the estimate without retaining a stale limit.
func (s *SQLiteStore) UpdateContextEstimate(ctx context.Context, id string, lastTotalTokens, lastMessageCount int) error {
	return s.UpdateContextEstimateWithWindow(ctx, id, lastTotalTokens, lastMessageCount, 0)
}

func (s *SQLiteStore) UpdateContextEstimateWithWindow(ctx context.Context, id string, lastTotalTokens, lastMessageCount, window int) error {
	if !s.hasLastTotalTokens || !s.hasLastMessageCount {
		return nil
	}
	return retryOnBusy(ctx, 5, func() error {
		_, err := s.db.ExecContext(ctx, `UPDATE sessions SET last_total_tokens = ?, last_message_count = ?, last_context_window = ?, updated_at = ? WHERE id = ?`, max(0, lastTotalTokens), max(0, lastMessageCount), max(0, window), time.Now(), id)
		return err
	})
}
