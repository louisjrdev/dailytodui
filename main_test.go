package main

import (
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
