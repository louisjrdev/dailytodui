package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestPartitionCompletedKeepsOpenItemsFirst(t *testing.T) {
	items := []todoItem{
		{Text: "done-1", Completed: true},
		{Text: "open-1"},
		{Text: "pending-1", Pending: true},
		{Text: "done-2", Completed: true},
		{Text: "open-2"},
	}

	got := partitionCompleted(items)
	wantOrder := []string{"pending-1", "open-1", "open-2", "done-1", "done-2"}
	for i, want := range wantOrder {
		if got[i].Text != want {
			t.Fatalf("index %d = %q, want %q", i, got[i].Text, want)
		}
	}
}

func TestMoveItemWithinSectionStopsAtCompletedBoundary(t *testing.T) {
	items := []todoItem{
		{Text: "open-1"},
		{Text: "open-2"},
		{Text: "done-1", Completed: true},
	}

	got, idx := moveItemWithinSection(items, 1, 1)
	if idx != 1 {
		t.Fatalf("idx = %d, want 1", idx)
	}
	if got[1].Text != "open-2" {
		t.Fatalf("item crossed section boundary: %#v", got)
	}
}

func TestMoveItemWithinSectionStopsAtPendingBoundary(t *testing.T) {
	items := []todoItem{
		{Text: "open-1"},
		{Text: "pending-1", Pending: true},
		{Text: "done-1", Completed: true},
	}

	got, idx := moveItemWithinSection(items, 1, -1)
	if idx != 1 {
		t.Fatalf("idx = %d, want 1", idx)
	}
	if got[1].Text != "pending-1" {
		t.Fatalf("item crossed section boundary: %#v", got)
	}
}

func TestParseDayInput(t *testing.T) {
	now := time.Date(2026, time.May, 7, 15, 4, 5, 0, time.UTC)
	got, err := parseDayInput("2026-05-09", now)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if got.Format(dayLayout) != "2026-05-09" {
		t.Fatalf("got %s", got.Format(dayLayout))
	}

	blank, err := parseDayInput("", now)
	if err != nil {
		t.Fatalf("blank parse error: %v", err)
	}
	if blank.Format(dayLayout) != "2026-05-07" {
		t.Fatalf("blank = %s", blank.Format(dayLayout))
	}
}

func TestCLIAddMultipleTodosWithStatus(t *testing.T) {
	now := time.Date(2026, time.May, 7, 9, 30, 0, 0, time.UTC)
	storageDir := t.TempDir()
	var stdout, stderr bytes.Buffer

	code := runCLI([]string{
		"add",
		"--storage-dir", storageDir,
		"--day", "2026-05-08",
		"--status", "pending",
		"--todo", "first task",
		"--todo", "second task",
	}, strings.NewReader(""), &stdout, &stderr, now)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}

	st, err := loadStore(storageDir)
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	items := st.Todos["2026-05-08"]
	if len(items) != 2 {
		t.Fatalf("len = %d, want 2", len(items))
	}
	for _, item := range items {
		if !item.Pending || item.Completed {
			t.Fatalf("item status = %#v, want pending", item)
		}
		if !item.CreatedAt.Equal(now) {
			t.Fatalf("created_at = %s, want %s", item.CreatedAt, now)
		}
	}
}

func TestCLICompleteByExactText(t *testing.T) {
	now := time.Date(2026, time.May, 7, 9, 30, 0, 0, time.UTC)
	storageDir := t.TempDir()
	st := store{Todos: map[string][]todoItem{
		"2026-05-08": {
			{Text: "open task", CreatedAt: now.Add(-time.Hour)},
			{Text: "still open", CreatedAt: now.Add(-time.Hour)},
			{Text: "done task", CreatedAt: now.Add(-time.Hour), Completed: true, DoneAt: now.Add(-time.Minute)},
		},
	}}
	if err := saveStore(storageDir, st); err != nil {
		t.Fatalf("save store: %v", err)
	}
	var stdout, stderr bytes.Buffer

	code := runCLI([]string{
		"complete",
		"--storage-dir", storageDir,
		"--day", "2026-05-08",
		"open task",
	}, strings.NewReader(""), &stdout, &stderr, now)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}

	got, err := loadStore(storageDir)
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	items := got.Todos["2026-05-08"]
	if items[0].Text != "still open" || items[1].Text != "open task" || items[2].Text != "done task" {
		t.Fatalf("completed item was not moved after open items: %#v", items)
	}
	if !items[1].Completed || !items[1].DoneAt.Equal(now) {
		t.Fatalf("completed item = %#v", items[1])
	}
}

func TestCLIStatusByIndex(t *testing.T) {
	now := time.Date(2026, time.May, 7, 9, 30, 0, 0, time.UTC)
	storageDir := t.TempDir()
	st := store{Todos: map[string][]todoItem{
		"2026-05-08": {
			{Text: "first", CreatedAt: now.Add(-time.Hour)},
			{Text: "second", CreatedAt: now.Add(-time.Hour)},
		},
	}}
	if err := saveStore(storageDir, st); err != nil {
		t.Fatalf("save store: %v", err)
	}
	var stdout, stderr bytes.Buffer

	code := runCLI([]string{
		"status",
		"--storage-dir", storageDir,
		"--day", "2026-05-08",
		"--index", "2",
		"pending",
	}, strings.NewReader(""), &stdout, &stderr, now)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}

	got, err := loadStore(storageDir)
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	items := got.Todos["2026-05-08"]
	if items[0].Text != "second" || !items[0].Pending {
		t.Fatalf("pending item should be first: %#v", items)
	}
}

func TestLastNonEmptyDayBefore(t *testing.T) {
	before := time.Date(2026, time.May, 7, 0, 0, 0, 0, time.UTC)
	todos := map[string][]todoItem{
		"2026-05-04": nil,
		"2026-05-05": {{Text: "older"}},
		"2026-05-06": {{Text: "latest"}},
		"2026-05-07": {{Text: "same day"}},
		"2026-05-08": {{Text: "future"}},
	}

	got, ok := lastNonEmptyDayBefore(todos, before)
	if !ok {
		t.Fatal("expected prior non-empty day")
	}
	if got.Format(dayLayout) != "2026-05-06" {
		t.Fatalf("got %s", got.Format(dayLayout))
	}
	if _, ok := lastNonEmptyDayBefore(map[string][]todoItem{}, before); ok {
		t.Fatal("expected no result for empty store")
	}
}

func TestRolloverIncompleteTodos(t *testing.T) {
	now := time.Date(2026, time.May, 7, 9, 30, 0, 0, time.UTC)
	target := []todoItem{{Text: "today existing", CreatedAt: now.Add(-time.Hour)}}
	source := []todoItem{
		{Text: "carry pending", Pending: true, CreatedAt: now.Add(-48 * time.Hour)},
		{Text: "carry open", CreatedAt: now.Add(-48 * time.Hour)},
		{Text: "done", Completed: true, DoneAt: now.Add(-47 * time.Hour)},
		{Text: "today existing", CreatedAt: now.Add(-48 * time.Hour)},
	}

	got, added := rolloverIncompleteTodos(target, source, now)
	if added != 2 {
		t.Fatalf("added = %d, want 2", added)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}

	wantOrder := []string{"carry pending", "today existing", "carry open"}
	for i, want := range wantOrder {
		if got[i].Text != want {
			t.Fatalf("index %d = %q, want %q", i, got[i].Text, want)
		}
	}
	if !got[0].Pending {
		t.Fatal("pending item lost pending state")
	}
	if got[1].CreatedAt != target[0].CreatedAt {
		t.Fatal("existing target item changed")
	}
	if !got[2].CreatedAt.Equal(now) {
		t.Fatalf("rolled item created_at = %s, want %s", got[2].CreatedAt, now)
	}
	if got[2].Completed {
		t.Fatal("rolled item should be incomplete")
	}
}
