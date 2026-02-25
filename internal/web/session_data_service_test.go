package web

import (
	"errors"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

type fakeStorage struct {
	instances []*session.Instance
	groups    []*session.GroupData
	loadErr   error
	closed    bool
}

func (f *fakeStorage) LoadWithGroups() ([]*session.Instance, []*session.GroupData, error) {
	if f.loadErr != nil {
		return nil, nil, f.loadErr
	}
	return f.instances, f.groups, nil
}

func (f *fakeStorage) Close() error {
	f.closed = true
	return nil
}

func TestSessionDataService_LoadMenuSnapshot(t *testing.T) {
	instWork := session.NewInstanceWithGroupAndTool("work-main", "/tmp/work", "work", "claude")
	instWork.ID = "sess-work"
	instWork.GroupPath = "work"
	instWork.Status = session.StatusRunning
	instWork.Order = 0

	instBackend := session.NewInstanceWithGroupAndTool("backend-task", "/tmp/backend", "work/backend", "gemini")
	instBackend.ID = "sess-backend"
	instBackend.GroupPath = "work/backend"
	instBackend.Status = session.StatusWaiting
	instBackend.Order = 0

	instPersonal := session.NewInstanceWithGroupAndTool("personal", "/tmp/personal", "personal", "shell")
	instPersonal.ID = "sess-personal"
	instPersonal.GroupPath = "personal"
	instPersonal.Status = session.StatusIdle
	instPersonal.Order = 0

	fake := &fakeStorage{
		instances: []*session.Instance{instWork, instBackend, instPersonal},
		groups: []*session.GroupData{
			{Name: "work", Path: "work", Expanded: true, Order: 0},
			{Name: "backend", Path: "work/backend", Expanded: true, Order: 0},
			{Name: "personal", Path: "personal", Expanded: true, Order: 1},
		},
	}

	fixedNow := time.Date(2026, time.February, 16, 0, 0, 0, 0, time.UTC)
	svc := &SessionDataService{
		profile: "test-profile",
		openStorage: func(profile string) (storageLoader, error) {
			if profile != "test-profile" {
				t.Fatalf("unexpected profile: %s", profile)
			}
			return fake, nil
		},
		now: func() time.Time { return fixedNow },
	}

	snapshot, err := svc.LoadMenuSnapshot()
	if err != nil {
		t.Fatalf("LoadMenuSnapshot() error = %v", err)
	}

	if !fake.closed {
		t.Fatal("expected storage Close() to be called")
	}

	if snapshot.Profile != "test-profile" {
		t.Fatalf("expected profile test-profile, got %s", snapshot.Profile)
	}
	if !snapshot.GeneratedAt.Equal(fixedNow) {
		t.Fatalf("expected generated time %s, got %s", fixedNow, snapshot.GeneratedAt)
	}
	if snapshot.TotalGroups != 3 {
		t.Fatalf("expected 3 groups, got %d", snapshot.TotalGroups)
	}
	if snapshot.TotalSessions != 3 {
		t.Fatalf("expected 3 sessions, got %d", snapshot.TotalSessions)
	}

	// Expected flattened order:
	// group work, session work, group work/backend, session backend, group personal, session personal
	if len(snapshot.Items) != 6 {
		t.Fatalf("expected 6 flattened items, got %d", len(snapshot.Items))
	}

	if snapshot.Items[0].Type != MenuItemTypeGroup || snapshot.Items[0].Path != "work" {
		t.Fatalf("unexpected first item: %+v", snapshot.Items[0])
	}
	if snapshot.Items[1].Type != MenuItemTypeSession || snapshot.Items[1].Session.ID != "sess-work" {
		t.Fatalf("unexpected second item: %+v", snapshot.Items[1])
	}
	if snapshot.Items[2].Type != MenuItemTypeGroup || snapshot.Items[2].Path != "work/backend" {
		t.Fatalf("unexpected third item: %+v", snapshot.Items[2])
	}
	if snapshot.Items[3].Type != MenuItemTypeSession || snapshot.Items[3].Session.ID != "sess-backend" {
		t.Fatalf("unexpected fourth item: %+v", snapshot.Items[3])
	}
	if snapshot.Items[4].Type != MenuItemTypeGroup || snapshot.Items[4].Path != "personal" {
		t.Fatalf("unexpected fifth item: %+v", snapshot.Items[4])
	}
	if snapshot.Items[5].Type != MenuItemTypeSession || snapshot.Items[5].Session.ID != "sess-personal" {
		t.Fatalf("unexpected sixth item: %+v", snapshot.Items[5])
	}

	if snapshot.Items[3].Session.Status != session.StatusWaiting {
		t.Fatalf("expected sess-backend waiting, got %s", snapshot.Items[3].Session.Status)
	}

	// Verify tier assignment: running → active, waiting → needsAttention, idle → idle
	if snapshot.Items[1].Session.Tier != "active" {
		t.Fatalf("expected sess-work tier=active, got %s", snapshot.Items[1].Session.Tier)
	}
	if snapshot.Items[3].Session.Tier != "needsAttention" {
		t.Fatalf("expected sess-backend tier=needsAttention, got %s", snapshot.Items[3].Session.Tier)
	}
	if snapshot.Items[3].Session.TierBadge != "approval" {
		t.Fatalf("expected sess-backend tierBadge=approval, got %s", snapshot.Items[3].Session.TierBadge)
	}
	if snapshot.Items[5].Session.Tier != "idle" {
		t.Fatalf("expected sess-personal tier=idle, got %s", snapshot.Items[5].Session.Tier)
	}
}

func TestAssignSessionTiers(t *testing.T) {
	now := time.Date(2026, time.February, 25, 12, 0, 0, 0, time.UTC)

	items := []MenuItem{
		{Type: MenuItemTypeGroup, Group: &MenuGroup{Name: "g"}},
		{Type: MenuItemTypeSession, Session: &MenuSession{ID: "waiting", Status: session.StatusWaiting}},
		{Type: MenuItemTypeSession, Session: &MenuSession{ID: "error", Status: session.StatusError}},
		{Type: MenuItemTypeSession, Session: &MenuSession{ID: "running", Status: session.StatusRunning}},
		{Type: MenuItemTypeSession, Session: &MenuSession{ID: "starting", Status: session.StatusStarting}},
		{Type: MenuItemTypeSession, Session: &MenuSession{ID: "recent", Status: session.StatusIdle, LastAccessedAt: now.Add(-10 * time.Minute)}},
		{Type: MenuItemTypeSession, Session: &MenuSession{ID: "old-idle", Status: session.StatusIdle, LastAccessedAt: now.Add(-2 * time.Hour)}},
		{Type: MenuItemTypeSession, Session: &MenuSession{ID: "zero-idle", Status: session.StatusIdle}},
	}

	assignSessionTiers(items, now)

	tests := []struct {
		idx       int
		wantTier  string
		wantBadge string
	}{
		{1, "needsAttention", "approval"},
		{2, "needsAttention", "error"},
		{3, "active", ""},
		{4, "active", ""},
		{5, "recent", ""},
		{6, "idle", ""},
		{7, "idle", ""},
	}

	for _, tc := range tests {
		s := items[tc.idx].Session
		if s.Tier != tc.wantTier {
			t.Errorf("item[%d] (%s): tier=%q, want %q", tc.idx, s.ID, s.Tier, tc.wantTier)
		}
		if s.TierBadge != tc.wantBadge {
			t.Errorf("item[%d] (%s): tierBadge=%q, want %q", tc.idx, s.ID, s.TierBadge, tc.wantBadge)
		}
	}

	// Group items should be untouched
	if items[0].Session != nil {
		t.Error("group item should not have a session")
	}
}

func TestSessionDataService_LoadMenuSnapshotOpenStorageError(t *testing.T) {
	svc := &SessionDataService{
		profile: "test",
		openStorage: func(_ string) (storageLoader, error) {
			return nil, errors.New("boom")
		},
		now: time.Now,
	}

	_, err := svc.LoadMenuSnapshot()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSessionDataService_LoadMenuSnapshotLoadError(t *testing.T) {
	fake := &fakeStorage{
		loadErr: errors.New("load failed"),
	}
	svc := &SessionDataService{
		profile: "test",
		openStorage: func(_ string) (storageLoader, error) {
			return fake, nil
		},
		now: time.Now,
	}

	_, err := svc.LoadMenuSnapshot()
	if err == nil {
		t.Fatal("expected error")
	}
	if !fake.closed {
		t.Fatal("expected storage Close() to be called")
	}
}

func TestSessionDataService_LoadMenuSnapshotIncludesDescendantsForCollapsedGroups(t *testing.T) {
	parent := session.NewInstanceWithGroupAndTool("parent", "/tmp/work", "work", "claude")
	parent.ID = "sess-parent"
	parent.GroupPath = "work"
	parent.Order = 0

	child := session.NewInstanceWithGroupAndTool("child", "/tmp/backend", "work/backend", "shell")
	child.ID = "sess-child"
	child.GroupPath = "work/backend"
	child.Order = 0

	fake := &fakeStorage{
		instances: []*session.Instance{parent, child},
		groups: []*session.GroupData{
			{Name: "work", Path: "work", Expanded: false, Order: 0},
			{Name: "backend", Path: "work/backend", Expanded: true, Order: 0},
		},
	}

	svc := &SessionDataService{
		profile: "test-profile",
		openStorage: func(profile string) (storageLoader, error) {
			if profile != "test-profile" {
				t.Fatalf("unexpected profile: %s", profile)
			}
			return fake, nil
		},
		now: time.Now,
	}

	snapshot, err := svc.LoadMenuSnapshot()
	if err != nil {
		t.Fatalf("LoadMenuSnapshot() error = %v", err)
	}

	if len(snapshot.Items) != 4 {
		t.Fatalf("expected 4 flattened items, got %d", len(snapshot.Items))
	}

	if snapshot.Items[0].Type != MenuItemTypeGroup || snapshot.Items[0].Group == nil {
		t.Fatalf("unexpected first item: %+v", snapshot.Items[0])
	}
	if snapshot.Items[0].Group.Path != "work" || snapshot.Items[0].Group.Expanded {
		t.Fatalf("expected collapsed work group, got %+v", snapshot.Items[0].Group)
	}

	if snapshot.Items[1].Type != MenuItemTypeSession || snapshot.Items[1].Session == nil || snapshot.Items[1].Session.ID != "sess-parent" {
		t.Fatalf("unexpected second item: %+v", snapshot.Items[1])
	}

	if snapshot.Items[2].Type != MenuItemTypeGroup || snapshot.Items[2].Group == nil {
		t.Fatalf("unexpected third item: %+v", snapshot.Items[2])
	}
	if snapshot.Items[2].Group.Path != "work/backend" || !snapshot.Items[2].Group.Expanded {
		t.Fatalf("expected expanded child group, got %+v", snapshot.Items[2].Group)
	}

	if snapshot.Items[3].Type != MenuItemTypeSession || snapshot.Items[3].Session == nil || snapshot.Items[3].Session.ID != "sess-child" {
		t.Fatalf("unexpected fourth item: %+v", snapshot.Items[3])
	}
}
