package server

import (
	"net/http"
	"time"
)

const (
	requestHealthBucketSize  = time.Hour
	requestHealthBucketCount = 7 * 24
)

// RequestHealthBucket summarizes the requests that started within one hour of the
// request health timeline. Success, Warning, and Failure always add up to Total.
type RequestHealthBucket struct {
	Start   time.Time `json:"start"`
	Total   int64     `json:"total"`
	Success int64     `json:"success"`
	Warning int64     `json:"warning"`
	Failure int64     `json:"failure"`
}

// handleAdminRequestHealth returns hourly request outcome counts for the last seven
// days, scoped to the request logs the user may read.
func (s *Server) handleAdminRequestHealth(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "audit", r.Method)
	if !ok {
		return
	}
	start, end := requestHealthWindow(time.Now())
	query := RequestLogQuery{Since: start, Until: end.Add(-time.Nanosecond)}
	s.scopeRequestLogQuery(user, &query)
	counts, err := s.store.CountRequestHealth(query)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"bucket_seconds": int(requestHealthBucketSize / time.Second),
		"window_start":   start,
		"window_end":     end,
		"data":           requestHealthBuckets(start, counts),
	})
}

// requestHealthWindow returns the hour-aligned window that ends with the hour
// containing now.
func requestHealthWindow(now time.Time) (time.Time, time.Time) {
	end := now.UTC().Truncate(requestHealthBucketSize).Add(requestHealthBucketSize)
	return end.Add(-requestHealthBucketCount * requestHealthBucketSize), end
}

// requestHealthBuckets lays the counts out on a dense series starting at start, so
// an empty bucket always means that no request was made in that hour.
func requestHealthBuckets(start time.Time, counts []RequestHealthCount) []RequestHealthBucket {
	buckets := make([]RequestHealthBucket, requestHealthBucketCount)
	for index := range buckets {
		buckets[index].Start = start.Add(time.Duration(index) * requestHealthBucketSize)
	}
	for _, count := range counts {
		bucketStart, err := time.Parse(time.RFC3339, count.Bucket)
		if err != nil || bucketStart.Before(start) {
			continue
		}
		index := int(bucketStart.Sub(start) / requestHealthBucketSize)
		if index >= len(buckets) {
			continue
		}
		buckets[index].Total += count.Total
		buckets[index].Warning += count.Warning
		buckets[index].Failure += count.Failure
	}
	for index := range buckets {
		bucket := &buckets[index]
		bucket.Success = bucket.Total - bucket.Warning - bucket.Failure
	}
	return buckets
}
