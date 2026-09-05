package server

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mattn/go-sqlite3"
	gormsqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

var errInjectedRedisBillingCommitFailure = errors.New("injected redis billing commit failure")

func TestRedisBillingFinalizeRetryDoesNotRollbackSettledResponseJob(t *testing.T) {
	store, project, key := newRedisBillingTestStore(t)
	tpm := int64(10)
	key.Limits.MaxConcurrency = 1
	if _, err := store.UpdateAPIKey(key.ID, APIKey{
		TokenLimitTPM: &tpm,
		TokenLimitSet: true,
		Limits:        key.Limits,
		LimitsSet:     true,
	}); err != nil {
		t.Fatal(err)
	}
	requestJSON := []byte(`{"model":"redis-billing-model","input":"background","background":true}`)
	job, err := store.CreateResponseJob(ResponseJob{
		ID:               NewID("resp"),
		ProjectID:        project.ID,
		APIKeyID:         key.ID,
		AttributedUserID: usageAttributionUserID(key, project),
		Model:            "redis-billing-model",
	}, requestJSON)
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := store.ClaimResponseJob("redis-worker", time.Second, time.Minute)
	if err != nil || !ok || claimed.ID != job.ID {
		t.Fatalf("claim response job: job=%+v ok=%v err=%v", claimed, ok, err)
	}
	call, retained, err := store.AdmitResponseJob(context.Background(), job.ID, "redis-worker", claimed.LeaseEpoch, key, "redis-billing-model", 5)
	if err != nil || !retained || !call.RedisBillingAdmitted {
		t.Fatalf("admit response job through Redis: retained=%v call=%+v err=%v", retained, call, err)
	}
	if _, settled, err := store.FinalizeResponseJob(call, job.ID, "redis-worker", claimed.LeaseEpoch, responseJobStatusSucceeded, []byte(`{"id":"resp_done"}`), RouteSelection{}, Usage{TotalTokens: 3}, http.StatusOK, "", "", "", "", time.Minute); err != nil || !settled {
		t.Fatalf("finalize response job: settled=%v err=%v", settled, err)
	}
	if _, settled, err := store.FinalizeResponseJob(call, job.ID, "redis-worker", claimed.LeaseEpoch, responseJobStatusSucceeded, []byte(`{"id":"resp_retry"}`), RouteSelection{}, Usage{TotalTokens: 3}, http.StatusOK, "", "", "", "", time.Minute); err != nil || settled {
		t.Fatalf("retry finalize should be ignored after terminal settlement: settled=%v err=%v", settled, err)
	}
	if _, err := store.StartCall(context.Background(), project, key, "redis-billing-model", 8); err == nil || AsHTTPError(err).Code != "api_key_tpm_exceeded" {
		t.Fatalf("settled response job tokens were rolled back by retry, got %v", err)
	}
	next, err := store.StartCall(context.Background(), project, key, "redis-billing-model", 7)
	if err != nil {
		t.Fatalf("settled response job should leave seven tokens available: %v", err)
	}
	store.FinishCall(next, RouteSelection{}, Usage{TotalTokens: 7}, http.StatusOK, "", "127.0.0.1", "redis-billing-test")
}

func TestRedisBillingResponseJobAdmissionRollsBackCommitFailure(t *testing.T) {
	store, failCommit, project, key := newRedisBillingCommitFailureStore(t)
	requestJSON := []byte(`{"model":"redis-billing-model","input":"background","background":true}`)
	job, err := store.CreateResponseJob(ResponseJob{
		ID:               NewID("resp"),
		ProjectID:        project.ID,
		APIKeyID:         key.ID,
		AttributedUserID: usageAttributionUserID(key, project),
		Model:            "redis-billing-model",
	}, requestJSON)
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := store.ClaimResponseJob("redis-worker", time.Second, time.Minute)
	if err != nil || !ok || claimed.ID != job.ID {
		t.Fatalf("claim response job: job=%+v ok=%v err=%v", claimed, ok, err)
	}

	failCommit.Store(true)
	_, retained, err := store.AdmitResponseJob(context.Background(), job.ID, "redis-worker", claimed.LeaseEpoch, key, "redis-billing-model", 5)
	failCommit.Store(false)
	if err == nil || !strings.Contains(err.Error(), errInjectedRedisBillingCommitFailure.Error()) || !retained {
		t.Fatalf("expected injected commit failure after Redis admission: retained=%v err=%v", retained, err)
	}
	rolledBack, ok, err := store.GetResponseJob(job.ID)
	if err != nil || !ok || rolledBack.Phase != responseJobPhaseClaimed || rolledBack.RequestID != "" {
		t.Fatalf("failed admission left durable response job state: job=%+v ok=%v err=%v", rolledBack, ok, err)
	}
	call, err := store.StartCall(context.Background(), project, key, "redis-billing-model", 10)
	if err != nil {
		t.Fatalf("commit-failed Redis admission retained quota or concurrency: %v", err)
	}
	store.FinishCall(call, RouteSelection{}, Usage{TotalTokens: 10}, http.StatusOK, "", "127.0.0.1", "redis-billing-test")
}

func TestRedisBillingImageJobAdmissionRollsBackCreateFailure(t *testing.T) {
	store, _, project, key := newRedisBillingCommitFailureStore(t)
	jobID := "img_redis_duplicate"
	if _, err := store.CreateImageJob(ImageJob{
		ID:        jobID,
		ProjectID: project.ID,
		APIKeyID:  key.ID,
		Model:     "redis-billing-model",
		Action:    "generate",
	}, "existing image prompt"); err != nil {
		t.Fatal(err)
	}

	_, _, err := store.CreateImageJobWithAdmission(context.Background(), project, key, "redis-billing-model", 5, ImageJob{
		ID:        jobID,
		ProjectID: project.ID,
		APIKeyID:  key.ID,
		Model:     "redis-billing-model",
		Action:    "generate",
	}, "duplicate image prompt")
	if err == nil {
		t.Fatal("expected duplicate image job creation to fail after Redis admission")
	}
	call, err := store.StartCall(context.Background(), project, key, "redis-billing-model", 10)
	if err != nil {
		t.Fatalf("create-failed Redis image admission retained quota or concurrency: %v", err)
	}
	store.FinishCall(call, RouteSelection{}, Usage{TotalTokens: 10}, http.StatusOK, "", "127.0.0.1", "redis-billing-test")
}

func TestRedisBillingImageJobAdmissionRollsBackCommitFailure(t *testing.T) {
	store, failCommit, project, key := newRedisBillingCommitFailureStore(t)
	jobID := "img_redis_commit_failure"

	failCommit.Store(true)
	_, _, err := store.CreateImageJobWithAdmission(context.Background(), project, key, "redis-billing-model", 5, ImageJob{
		ID:        jobID,
		ProjectID: project.ID,
		APIKeyID:  key.ID,
		Model:     "redis-billing-model",
		Action:    "generate",
	}, "commit failure image prompt")
	failCommit.Store(false)
	if err == nil || !strings.Contains(err.Error(), errInjectedRedisBillingCommitFailure.Error()) {
		t.Fatalf("expected injected image job commit failure after Redis admission, got %v", err)
	}
	if _, ok := store.GetImageJob(jobID); ok {
		t.Fatal("commit-failed image job admission left a durable job row")
	}
	call, err := store.StartCall(context.Background(), project, key, "redis-billing-model", 10)
	if err != nil {
		t.Fatalf("commit-failed Redis image admission retained quota or concurrency: %v", err)
	}
	store.FinishCall(call, RouteSelection{}, Usage{TotalTokens: 10}, http.StatusOK, "", "127.0.0.1", "redis-billing-test")
}

func newRedisBillingCommitFailureStore(t *testing.T) (*GormStore, *atomic.Bool, Project, APIKey) {
	t.Helper()
	redisServer := miniredis.RunT(t)
	failCommit := &atomic.Bool{}
	driverName := NewID("sqlite_redis_billing_commit_failure")
	sql.Register(driverName, commitFailureSQLiteDriver{failCommit: failCommit})
	database, err := gorm.Open(gormsqlite.New(gormsqlite.Config{
		DriverName: driverName,
		DSN:        filepath.Join(t.TempDir(), "redis-billing-commit-failure.db"),
	}), &gorm.Config{
		TranslateError: true,
		Logger:         gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.Exec("PRAGMA foreign_keys = ON").Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec("PRAGMA busy_timeout = 5000").Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateSchemaObjects(database, "sqlite"); err != nil {
		t.Fatal(err)
	}
	for _, statement := range meteringMigration().Statements {
		if err := database.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	billingRedis, err := newRedisBillingCoordinator(context.Background(), "redis://"+redisServer.Addr()+"/0", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = billingRedis.client.Close() })
	store := &GormStore{
		db:               database,
		analyticsDB:      database,
		mu:               &sync.Mutex{},
		leaseHeartbeats:  &sync.Map{},
		heartbeatState:   new(atomic.Int32),
		lastUsed:         newLastUsedThrottle(),
		modelLabels:      newModelLabelCache(),
		secretKey:        "redis-billing-test-secret",
		dbDriver:         "sqlite",
		inFlightLeaseTTL: 2 * time.Second,
		clusterLockTTL:   2 * time.Second,
		billingRedis:     billingRedis,
	}
	project := store.CreateProject(Project{ID: "prj_redis_commit_failure", Name: "Redis commit failure", Status: StatusActive})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{ID: "key_redis_commit_failure", Name: "Redis commit failure", Status: StatusActive}, "thk_redis_commit_failure")
	if err != nil {
		t.Fatal(err)
	}
	tpm := int64(10)
	key.Limits.MaxConcurrency = 1
	key, err = store.UpdateAPIKey(key.ID, APIKey{
		TokenLimitTPM: &tpm,
		TokenLimitSet: true,
		Limits:        key.Limits,
		LimitsSet:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "redis-billing-model", Modality: "chat", Status: StatusActive})
	return store, failCommit, project, key
}

type commitFailureSQLiteDriver struct {
	failCommit *atomic.Bool
}

func (d commitFailureSQLiteDriver) Open(name string) (driver.Conn, error) {
	connection, err := (&sqlite3.SQLiteDriver{}).Open(name)
	if err != nil {
		return nil, err
	}
	return commitFailureConn{Conn: connection, failCommit: d.failCommit}, nil
}

type commitFailureConn struct {
	driver.Conn
	failCommit *atomic.Bool
}

func (c commitFailureConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c commitFailureConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if beginner, ok := c.Conn.(driver.ConnBeginTx); ok {
		tx, err := beginner.BeginTx(ctx, opts)
		if err != nil {
			return nil, err
		}
		return commitFailureTx{Tx: tx, failCommit: c.failCommit}, nil
	}
	return nil, errors.New("commit failure test driver requires driver.ConnBeginTx")
}

func (c commitFailureConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if preparer, ok := c.Conn.(driver.ConnPrepareContext); ok {
		return preparer.PrepareContext(ctx, query)
	}
	return c.Conn.Prepare(query)
}

func (c commitFailureConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if execer, ok := c.Conn.(driver.ExecerContext); ok {
		return execer.ExecContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}

func (c commitFailureConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if queryer, ok := c.Conn.(driver.QueryerContext); ok {
		return queryer.QueryContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}

func (c commitFailureConn) Ping(ctx context.Context) error {
	if pinger, ok := c.Conn.(driver.Pinger); ok {
		return pinger.Ping(ctx)
	}
	return nil
}

func (c commitFailureConn) CheckNamedValue(value *driver.NamedValue) error {
	if checker, ok := c.Conn.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(value)
	}
	return driver.ErrSkip
}

type commitFailureTx struct {
	driver.Tx
	failCommit *atomic.Bool
}

func (tx commitFailureTx) Commit() error {
	if tx.failCommit.Load() {
		_ = tx.Tx.Rollback()
		return errInjectedRedisBillingCommitFailure
	}
	return tx.Tx.Commit()
}
