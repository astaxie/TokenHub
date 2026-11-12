package analytics

import (
	"errors"
	"time"
)

type CursorQuery struct {
	ProjectID   string
	UserID      string
	APIKeyID    string
	ProviderID  string
	Model       string
	Status      string
	Granularity string
	GroupBy     []string
}

type Cursor struct {
	Version         int
	AfterAt         time.Time
	AfterID         string
	From            time.Time
	Through         time.Time
	Kind            string
	Offset          int
	QueryKey        string
	Query           CursorQuery
	Checkpoint      int64
	AfterCheckpoint int64
	Incremental     bool
}

func ValidateCursor(cursor Cursor) error {
	if cursor.Version != CursorVersion || cursor.From.IsZero() || cursor.Through.IsZero() ||
		!cursor.From.Before(cursor.Through) || cursor.Query.Granularity == "" ||
		cursor.Checkpoint < 0 || cursor.AfterCheckpoint < 0 || cursor.AfterCheckpoint > cursor.Checkpoint {
		return errors.New("cursor is invalid")
	}
	switch cursor.Kind {
	case "request":
		if cursor.AfterAt.IsZero() || cursor.AfterID == "" {
			return errors.New("cursor is invalid")
		}
	case "aggregate":
		if cursor.Offset <= 0 {
			return errors.New("cursor is invalid")
		}
	case "watermark":
	default:
		return errors.New("cursor is invalid")
	}
	return nil
}
