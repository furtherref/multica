package handler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/daemonws"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// fakeLivenessStore lets tests drive every Available / Touch / IsAliveBatch
// branch of recordHeartbeat without spinning up Redis. It records call counts
// so we can assert the gate behavior without any DB-time dependence.
type fakeLivenessStore struct {
	mu          sync.Mutex
	available   bool
	touchErr    error
	touched     []string
	aliveResult map[string]bool
	aliveOK     bool
	forgotten   []string
}

func (f *fakeLivenessStore) Available() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.available
}

func (f *fakeLivenessStore) Touch(_ context.Context, runtimeID string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.touched = append(f.touched, runtimeID)
	return f.touchErr
}

func (f *fakeLivenessStore) IsAliveBatch(_ context.Context, ids []string) (map[string]bool, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.aliveOK {
		return nil, false
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = f.aliveResult[id]
	}
	return out, true
}

func (f *fakeLivenessStore) Forget(_ context.Context, runtimeID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forgotten = append(f.forgotten, runtimeID)
}

func (f *fakeLivenessStore) touchCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.touched)
}

type recordingHeartbeatScheduler struct {
	ids []pgtype.UUID
	err error
}

func (s *recordingHeartbeatScheduler) Schedule(_ context.Context, id pgtype.UUID) error {
	s.ids = append(s.ids, id)
	return s.err
}

// readRuntimeRow returns the fresh agent_runtime row for assertions.
func readRuntimeRow(t *testing.T, runtimeID string) (status string, lastSeen time.Time, updatedAt time.Time) {
	t.Helper()
	if err := testPool.QueryRow(context.Background(),
		`SELECT status, last_seen_at, updated_at FROM agent_runtime WHERE id = $1`, runtimeID,
	).Scan(&status, &lastSeen, &updatedAt); err != nil {
		t.Fatalf("read runtime row: %v", err)
	}
	return
}

func setRuntimeLastSeenAt(t *testing.T, runtimeID string, when time.Time) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(),
		`UPDATE agent_runtime SET last_seen_at = $1 WHERE id = $2`, when, runtimeID,
	); err != nil {
		t.Fatalf("force last_seen_at: %v", err)
	}
}

func setRuntimeStatus(t *testing.T, runtimeID, status string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(),
		`UPDATE agent_runtime SET status = $1 WHERE id = $2`, status, runtimeID,
	); err != nil {
		t.Fatalf("force status: %v", err)
	}
}

// loadRuntime is a thin wrapper around the sqlc query to keep the test bodies
// short.
func loadRuntime(t *testing.T, runtimeID string) db.AgentRuntime {
	t.Helper()
	uuid, err := pgUUID(runtimeID)
	if err != nil {
		t.Fatalf("parse runtime id: %v", err)
	}
	rt, err := testHandler.Queries.GetAgentRuntime(context.Background(), uuid)
	if err != nil {
		t.Fatalf("GetAgentRuntime: %v", err)
	}
	return rt
}

func pgUUID(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return u, err
	}
	return u, nil
}

func TestRecordHeartbeatLeaseThrottlesDBScheduling(t *testing.T) {
	runtimeID := uuid.NewString()
	fake := &fakeLivenessStore{available: true, aliveOK: true}
	scheduler := &recordingHeartbeatScheduler{}
	h := &Handler{LivenessStore: fake, HeartbeatScheduler: scheduler}
	lease := daemonws.NewRuntimeLease("workspace-1", "", "online", time.Now().Add(-2*runtimeHeartbeatDBFlushInterval), true)

	if err := h.recordHeartbeatLease(context.Background(), runtimeID, lease); err != nil {
		t.Fatalf("first recordHeartbeatLease: %v", err)
	}
	if err := h.recordHeartbeatLease(context.Background(), runtimeID, lease); err != nil {
		t.Fatalf("second recordHeartbeatLease: %v", err)
	}

	if len(scheduler.ids) != 1 {
		t.Fatalf("scheduled DB writes = %d, want 1 within one flush window", len(scheduler.ids))
	}
	if fake.touchCount() != 2 {
		t.Fatalf("Redis touches = %d, want 2", fake.touchCount())
	}
	state := lease.Snapshot()
	if !state.LastSeenAtValid || time.Since(state.LastSeenAt) > time.Second {
		t.Fatalf("lease DB watermark was not advanced: %+v", state)
	}
}

func TestRecordHeartbeatLeaseScheduleFailureKeepsStaleWatermark(t *testing.T) {
	runtimeID := uuid.NewString()
	fake := &fakeLivenessStore{available: true, aliveOK: true}
	injected := errors.New("injected schedule failure")
	scheduler := &recordingHeartbeatScheduler{err: injected}
	h := &Handler{LivenessStore: fake, HeartbeatScheduler: scheduler}
	stale := time.Now().Add(-2 * runtimeHeartbeatDBFlushInterval)
	lease := daemonws.NewRuntimeLease("workspace-1", "", "online", stale, true)

	if err := h.recordHeartbeatLease(context.Background(), runtimeID, lease); !errors.Is(err, injected) {
		t.Fatalf("recordHeartbeatLease error = %v, want injected failure", err)
	}
	if got := lease.Snapshot().LastSeenAt; !got.Equal(stale) {
		t.Fatalf("failed schedule advanced lease watermark: got %s want %s", got, stale)
	}
}

func TestRecordHeartbeatLeaseOfflineTransitionIsSynchronous(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := createRuntimeLocalSkillTestRuntime(t, testUserID)
	setRuntimeStatus(t, runtimeID, "offline")
	fake := &fakeLivenessStore{available: true, aliveOK: true}
	scheduler := NewBatchedHeartbeatScheduler(testHandler.Queries, time.Hour, nil)
	h := *testHandler
	h.LivenessStore = fake
	h.HeartbeatScheduler = scheduler
	lease := daemonws.NewRuntimeLease(testWorkspaceID, "", "offline", time.Now(), true)

	if err := h.recordHeartbeatLease(context.Background(), runtimeID, lease); err != nil {
		t.Fatalf("recordHeartbeatLease: %v", err)
	}

	status, _, _ := readRuntimeRow(t, runtimeID)
	if status != "online" {
		t.Fatalf("status = %q, want online before heartbeat returns", status)
	}
	if got := scheduler.PendingCount(); got != 0 {
		t.Fatalf("offline transition was deferred to batch queue: pending=%d", got)
	}
	if state := lease.Snapshot(); state.Status != "online" || !state.LastSeenAtValid {
		t.Fatalf("lease state was not advanced after synchronous recovery: %+v", state)
	}
}

// TestRecordHeartbeat_NoopStoreAlwaysWritesDB confirms that without a Redis
// LivenessStore the heartbeat path keeps the legacy behavior: every call
// bumps last_seen_at on the DB row.
func TestRecordHeartbeat_NoopStoreAlwaysWritesDB(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := createRuntimeLocalSkillTestRuntime(t, testUserID)

	orig := testHandler.LivenessStore
	testHandler.LivenessStore = NewNoopLivenessStore()
	t.Cleanup(func() { testHandler.LivenessStore = orig })

	// Pin last_seen_at to "just now" to ensure the DB-flush condition is not
	// what's driving the write.
	setRuntimeLastSeenAt(t, runtimeID, time.Now())
	rt := loadRuntime(t, runtimeID)
	before := rt.LastSeenAt.Time

	time.Sleep(50 * time.Millisecond)

	if err := testHandler.recordHeartbeat(context.Background(), rt); err != nil {
		t.Fatalf("recordHeartbeat: %v", err)
	}

	_, lastSeen, _ := readRuntimeRow(t, runtimeID)
	if !lastSeen.After(before) {
		t.Fatalf("noop-store heartbeat did not bump last_seen_at: before=%s after=%s", before, lastSeen)
	}
}

// TestRecordHeartbeat_RedisAvailableSkipsDBWithinFlushWindow confirms the hot
// path: when Redis is the source of truth and the row is fresh, the heartbeat
// touches Redis but does NOT rewrite the DB row.
func TestRecordHeartbeat_RedisAvailableSkipsDBWithinFlushWindow(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := createRuntimeLocalSkillTestRuntime(t, testUserID)

	fake := &fakeLivenessStore{available: true, aliveOK: true}
	orig := testHandler.LivenessStore
	testHandler.LivenessStore = fake
	t.Cleanup(func() { testHandler.LivenessStore = orig })

	// Pin last_seen_at to "just now" so we are inside the flush window.
	setRuntimeLastSeenAt(t, runtimeID, time.Now())
	rt := loadRuntime(t, runtimeID)
	before := rt.LastSeenAt.Time

	if err := testHandler.recordHeartbeat(context.Background(), rt); err != nil {
		t.Fatalf("recordHeartbeat: %v", err)
	}

	if fake.touchCount() != 1 {
		t.Fatalf("expected exactly one Touch, got %d", fake.touchCount())
	}
	_, lastSeen, _ := readRuntimeRow(t, runtimeID)
	if !lastSeen.Equal(before) {
		t.Fatalf("DB last_seen_at should not have been rewritten within flush window: before=%s after=%s", before, lastSeen)
	}
}

// TestRecordHeartbeat_DBFlushOnStaleRow confirms the DB summary flush:
// even with Redis healthy, a row whose last_seen_at exceeds the flush
// interval gets a write so the UI's display value stays bounded.
func TestRecordHeartbeat_DBFlushOnStaleRow(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := createRuntimeLocalSkillTestRuntime(t, testUserID)

	fake := &fakeLivenessStore{available: true, aliveOK: true}
	orig := testHandler.LivenessStore
	testHandler.LivenessStore = fake
	t.Cleanup(func() { testHandler.LivenessStore = orig })

	// Push last_seen_at past the flush threshold.
	stale := time.Now().Add(-2 * runtimeHeartbeatDBFlushInterval)
	setRuntimeLastSeenAt(t, runtimeID, stale)
	rt := loadRuntime(t, runtimeID)

	if err := testHandler.recordHeartbeat(context.Background(), rt); err != nil {
		t.Fatalf("recordHeartbeat: %v", err)
	}

	_, lastSeen, _ := readRuntimeRow(t, runtimeID)
	if !lastSeen.After(stale.Add(time.Minute)) {
		t.Fatalf("DB last_seen_at should have been flushed: stale=%s after=%s", stale, lastSeen)
	}
}

// TestRecordHeartbeat_OfflineToOnlineForcesDBWrite confirms that an offline
// row's first heartbeat always rewrites the DB to flip status, even with
// Redis healthy.
func TestRecordHeartbeat_OfflineToOnlineForcesDBWrite(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := createRuntimeLocalSkillTestRuntime(t, testUserID)

	fake := &fakeLivenessStore{available: true, aliveOK: true}
	orig := testHandler.LivenessStore
	testHandler.LivenessStore = fake
	t.Cleanup(func() { testHandler.LivenessStore = orig })

	setRuntimeStatus(t, runtimeID, "offline")
	// Keep last_seen_at fresh so the DB-flush condition is not what's
	// driving the write — only the offline→online transition is.
	setRuntimeLastSeenAt(t, runtimeID, time.Now())
	rt := loadRuntime(t, runtimeID)
	if rt.Status != "offline" {
		t.Fatalf("setup: status = %q, want offline", rt.Status)
	}

	if err := testHandler.recordHeartbeat(context.Background(), rt); err != nil {
		t.Fatalf("recordHeartbeat: %v", err)
	}

	status, _, _ := readRuntimeRow(t, runtimeID)
	if status != "online" {
		t.Fatalf("expected status=online after offline→online heartbeat, got %q", status)
	}
}

// TestRecordHeartbeat_TouchErrorFallsBackToDB confirms graceful degradation:
// if Redis Touch errors, the heartbeat still writes the DB so the sweeper's
// DB-only fallback path observes a fresh last_seen_at.
func TestRecordHeartbeat_TouchErrorFallsBackToDB(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := createRuntimeLocalSkillTestRuntime(t, testUserID)

	fake := &fakeLivenessStore{
		available: true,
		touchErr:  errors.New("simulated redis outage"),
	}
	orig := testHandler.LivenessStore
	testHandler.LivenessStore = fake
	t.Cleanup(func() { testHandler.LivenessStore = orig })

	setRuntimeLastSeenAt(t, runtimeID, time.Now())
	rt := loadRuntime(t, runtimeID)
	before := rt.LastSeenAt.Time

	time.Sleep(50 * time.Millisecond)

	if err := testHandler.recordHeartbeat(context.Background(), rt); err != nil {
		t.Fatalf("recordHeartbeat: %v", err)
	}

	_, lastSeen, _ := readRuntimeRow(t, runtimeID)
	if !lastSeen.After(before) {
		t.Fatalf("Touch failure should have fallen back to a DB write: before=%s after=%s", before, lastSeen)
	}
}

// TestRecordHeartbeat_SweeperRaceRecoversOnline pins the regression for the
// status-snapshot race: rt.Status was read from a prior SELECT, but the
// sweeper can flip the row to offline between that SELECT and the heartbeat's
// write. Without the affected-rows fallback in recordHeartbeat, the heartbeat
// would only bump last_seen_at and leave the row stuck offline. The legacy
// UpdateAgentRuntimeHeartbeat always re-asserted status='online', so this
// regression test guards the new SELECT/Touch/MarkOnline path against the
// same scenario.
func TestRecordHeartbeat_SweeperRaceRecoversOnline(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := createRuntimeLocalSkillTestRuntime(t, testUserID)

	// Force the noop store so recordHeartbeat takes the DB-write path
	// without any Redis interference. The race is independent of the
	// liveness store — it lives entirely between the rt.Status snapshot
	// and the DB UPDATE.
	orig := testHandler.LivenessStore
	testHandler.LivenessStore = NewNoopLivenessStore()
	t.Cleanup(func() { testHandler.LivenessStore = orig })

	// Snapshot the runtime while it is still online.
	rt := loadRuntime(t, runtimeID)
	if rt.Status != "online" {
		t.Fatalf("setup: runtime should be online, got %q", rt.Status)
	}

	// Simulate the sweeper flipping the row to offline between the
	// snapshot and the heartbeat's UPDATE.
	setRuntimeStatus(t, runtimeID, "offline")

	if err := testHandler.recordHeartbeat(context.Background(), rt); err != nil {
		t.Fatalf("recordHeartbeat: %v", err)
	}

	status, lastSeen, _ := readRuntimeRow(t, runtimeID)
	if status != "online" {
		t.Fatalf("expected sweeper-raced runtime to recover online, got %q", status)
	}
	if time.Since(lastSeen) > 30*time.Second {
		t.Fatalf("last_seen_at not refreshed: %s ago", time.Since(lastSeen))
	}
}

// TestRecordHeartbeat_SuspendedOwnerDoesNotResurrectRuntime pins the
// suspension race: a heartbeat that passed auth BEFORE the owner was
// suspended (in-flight request, or the WS handler's already-loaded runtime
// row) must not write the runtime back online after suspension force-offlined
// it. recordHeartbeat re-checks the owner's account status before scheduling
// the liveness write.
func TestRecordHeartbeat_SuspendedOwnerDoesNotResurrectRuntime(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	owner, err := testHandler.Queries.CreateUser(ctx, db.CreateUserParams{
		Name:  "Suspended Heartbeat Owner",
		Email: "suspended-heartbeat-owner@multica.ai",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, uuidToString(owner.ID))
	})

	runtimeID := createRuntimeLocalSkillTestRuntime(t, uuidToString(owner.ID))
	// The runtime row the in-flight heartbeat holds predates the suspension:
	// load it while the owner is still active…
	rt := loadRuntime(t, runtimeID)

	// …then suspend the owner and force the runtime offline, as the admin
	// endpoint's transaction does.
	if _, err := testHandler.Queries.SetUserAccountStatus(ctx, db.SetUserAccountStatusParams{
		ID:            owner.ID,
		AccountStatus: auth.AccountStatusSuspended,
	}); err != nil {
		t.Fatalf("SetUserAccountStatus: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE agent_runtime SET status = 'offline' WHERE id = $1`, runtimeID); err != nil {
		t.Fatalf("force offline: %v", err)
	}

	prevGuard := testHandler.AccountGuard
	testHandler.AccountGuard = &auth.AccountGuard{Queries: testHandler.Queries}
	t.Cleanup(func() { testHandler.AccountGuard = prevGuard })

	origStore := testHandler.LivenessStore
	testHandler.LivenessStore = NewNoopLivenessStore()
	t.Cleanup(func() { testHandler.LivenessStore = origStore })

	// rt.Status is the stale pre-suspension "online" snapshot; the write it
	// schedules would resurrect the row without the owner-status re-check.
	if err := testHandler.recordHeartbeat(ctx, rt); err != nil {
		t.Fatalf("recordHeartbeat: %v", err)
	}

	status, _, _ := readRuntimeRow(t, runtimeID)
	if status != "offline" {
		t.Fatalf("runtime status = %q, want offline (suspended owner's heartbeat must not resurrect it)", status)
	}
}

// TestMarkAgentRuntimeOnlineRefusesSuspendedOwner pins the write-side TOCTOU
// closure: even when the app-level owner check raced ahead of the suspension
// commit, the UPDATE itself refuses to flip a suspended owner's runtime back
// online (zero rows → pgx.ErrNoRows), and the scheduler passes that through
// as the runtime-gone signal instead of resurrecting the row.
func TestMarkAgentRuntimeOnlineRefusesSuspendedOwner(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	owner, err := testHandler.Queries.CreateUser(ctx, db.CreateUserParams{
		Name:  "Suspended Mark Online Owner",
		Email: "suspended-mark-online-owner@multica.ai",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, uuidToString(owner.ID))
	})
	runtimeID := createRuntimeLocalSkillTestRuntime(t, uuidToString(owner.ID))
	rt := loadRuntime(t, runtimeID)

	if _, err := testHandler.Queries.SetUserAccountStatus(ctx, db.SetUserAccountStatusParams{
		ID:            owner.ID,
		AccountStatus: auth.AccountStatusSuspended,
	}); err != nil {
		t.Fatalf("SetUserAccountStatus: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE agent_runtime SET status = 'offline' WHERE id = $1`, runtimeID); err != nil {
		t.Fatalf("force offline: %v", err)
	}

	if _, err := testHandler.Queries.MarkAgentRuntimeOnline(ctx, rt.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("MarkAgentRuntimeOnline err = %v, want pgx.ErrNoRows (flip refused)", err)
	}
	status, _, _ := readRuntimeRow(t, runtimeID)
	if status != "offline" {
		t.Fatalf("runtime status = %q, want offline", status)
	}

	// The scheduler surfaces the refused flip as pgx.ErrNoRows, the same
	// signal a deleted row produces: the heartbeat path answers it by
	// invalidating the daemon's connection, which is exactly what a
	// suspension wants (the suspend endpoint severs daemon sockets too). The
	// row itself must stay offline.
	sched := NewPassthroughHeartbeatScheduler(testHandler.Queries)
	if err := sched.Schedule(ctx, rt.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("Schedule err = %v, want pgx.ErrNoRows (refused flip must not resurrect the row)", err)
	}
	if status, _, _ := readRuntimeRow(t, runtimeID); status != "offline" {
		t.Fatalf("runtime status after Schedule = %q, want offline", status)
	}

	// Restore the owner: the flip works again.
	if _, err := testHandler.Queries.SetUserAccountStatus(ctx, db.SetUserAccountStatusParams{
		ID:            owner.ID,
		AccountStatus: auth.AccountStatusActive,
	}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := testHandler.Queries.MarkAgentRuntimeOnline(ctx, rt.ID); err != nil {
		t.Fatalf("MarkAgentRuntimeOnline after restore: %v", err)
	}
	status, _, _ = readRuntimeRow(t, runtimeID)
	if status != "online" {
		t.Fatalf("runtime status = %q, want online after restore", status)
	}
}

// TestRuntimeOwnerSuspendedGuard covers the shared per-request guard the
// claim paths use to neutralize stale mdt_ cache entries.
func TestRuntimeOwnerSuspendedGuard(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	owner, err := testHandler.Queries.CreateUser(ctx, db.CreateUserParams{
		Name:  "Claim Guard Owner",
		Email: "claim-guard-owner@multica.ai",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, uuidToString(owner.ID))
	})
	runtimeID := createRuntimeLocalSkillTestRuntime(t, uuidToString(owner.ID))
	rt := loadRuntime(t, runtimeID)

	prevGuard := testHandler.AccountGuard
	testHandler.AccountGuard = &auth.AccountGuard{Queries: testHandler.Queries}
	t.Cleanup(func() { testHandler.AccountGuard = prevGuard })

	if testHandler.runtimeOwnerSuspended(ctx, rt) {
		t.Fatal("active owner must not be reported suspended")
	}
	if _, err := testHandler.Queries.SetUserAccountStatus(ctx, db.SetUserAccountStatusParams{
		ID:            owner.ID,
		AccountStatus: auth.AccountStatusSuspended,
	}); err != nil {
		t.Fatalf("SetUserAccountStatus: %v", err)
	}
	if !testHandler.runtimeOwnerSuspended(ctx, rt) {
		t.Fatal("suspended owner must be reported suspended")
	}
	if testHandler.runtimeOwnerSuspended(ctx, db.AgentRuntime{}) {
		t.Fatal("ownerless runtime must never be reported suspended")
	}
}
