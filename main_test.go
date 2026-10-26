package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPartitionCompletedKeepsTerminalItemsLast(t *testing.T) {
	items := []todoItem{
		{Text: "done-1", Completed: true},
		{Text: "open-1"},
		{Text: "pending-1", Pending: true},
		{Text: "important-1", Important: true},
		{Text: "cancelled-1", Cancelled: true},
		{Text: "done-2", Completed: true},
		{Text: "open-2"},
	}

	got := partitionCompleted(items)
	wantOrder := []string{"important-1", "pending-1", "open-1", "open-2", "done-1", "done-2", "cancelled-1"}
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

func TestCancelledToggleUpdatesStatusAndFooter(t *testing.T) {
	now := time.Date(2026, time.May, 7, 9, 30, 0, 0, time.UTC)
	storageDir := t.TempDir()
	m := model{
		store: store{Todos: map[string][]todoItem{
			dateKey(now): {{Text: "skip this", CreatedAt: now}},
		}},
		day:        now,
		storageDir: storageDir,
	}
	if err := saveStore(storageDir, m.store); err != nil {
		t.Fatalf("save store: %v", err)
	}

	updated, _ := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	got := updated.(model)
	item := got.itemsForDay()[0]
	if !item.Cancelled || item.Pending || item.Completed || item.DoneAt.IsZero() {
		t.Fatalf("item = %#v, want cancelled terminal status", item)
	}
	view := got.View()
	if !strings.Contains(view, "[-] skip this") || !strings.Contains(view, "c cancel") {
		t.Fatalf("view is missing cancelled item or key hint: %q", view)
	}
}

func TestImportantToggleMovesItemToTop(t *testing.T) {
	now := time.Date(2026, time.May, 7, 9, 30, 0, 0, time.UTC)
	storageDir := t.TempDir()
	m := model{
		store: store{Todos: map[string][]todoItem{
			dateKey(now): {
				{Text: "first", CreatedAt: now},
				{Text: "urgent", CreatedAt: now.Add(time.Second)},
			},
		}},
		day:        now,
		cursor:     1,
		storageDir: storageDir,
	}
	if err := saveStore(storageDir, m.store); err != nil {
		t.Fatalf("save store: %v", err)
	}

	updated, _ := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	got := updated.(model)
	items := got.itemsForDay()
	if items[0].Text != "urgent" || !items[0].Important {
		t.Fatalf("items = %#v, want important item first", items)
	}
	if !strings.Contains(got.View(), "i important") {
		t.Fatal("footer is missing important key hint")
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

func TestConfigCommandEnablesObsidianSync(t *testing.T) {
	storageDir := t.TempDir()
	vaultPath := t.TempDir()
	var stdout, stderr bytes.Buffer

	code := runCLI([]string{
		"config",
		"--storage-dir", storageDir,
		"--obsidian-sync",
		"--obsidian-vault", vaultPath,
		"--obsidian-daily-note", "Daily Notes/{date}.md",
	}, strings.NewReader(""), &stdout, &stderr, time.Now())
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}

	config, err := loadConfig(storageDir)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !config.Obsidian.Enabled || config.Obsidian.VaultPath != vaultPath || config.Obsidian.DailyNotePath != "Daily Notes/{date}.md" {
		t.Fatalf("config = %#v", config)
	}
}

func TestObsidianDailyNotePathStaysWithinVault(t *testing.T) {
	_, err := obsidianDailyNotePath(obsidianConfig{VaultPath: t.TempDir(), DailyNotePath: "../{date}.md"}, time.Now(), false)
	if err == nil {
		t.Fatal("expected path outside the vault to be rejected")
	}
}

func TestCLIObsidianSyncWritesEditsAndReadsNote(t *testing.T) {
	now := time.Date(2026, time.May, 7, 9, 30, 0, 0, time.UTC)
	storageDir := t.TempDir()
	vaultPath := t.TempDir()
	if err := saveConfig(storageDir, appConfig{Obsidian: obsidianConfig{Enabled: true, VaultPath: vaultPath}}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"add", "--storage-dir", storageDir, "--day", "2026-05-07", "from app"}, strings.NewReader(""), &stdout, &stderr, now); code != 0 {
		t.Fatalf("add code = %d, stderr = %s", code, stderr.String())
	}

	notePath := filepath.Join(vaultPath, "2026-05-07.md")
	data, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatalf("read synced note: %v", err)
	}
	if got, want := string(data), "## TODO\n- [ ] from app\n"; got != want {
		t.Fatalf("note = %q, want %q", got, want)
	}
	if err := os.WriteFile(notePath, []byte("## TODO\n- [x] edited in Obsidian\n"), 0o600); err != nil {
		t.Fatalf("edit note: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := runCLI([]string{"list", "--storage-dir", storageDir, "--day", "2026-05-07"}, strings.NewReader(""), &stdout, &stderr, now); code != 0 {
		t.Fatalf("list code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "[x] edited in Obsidian") {
		t.Fatalf("list output = %q", stdout.String())
	}
}

func TestObsidianSyncReadsAndWritesTODOSection(t *testing.T) {
	now := time.Date(2026, time.May, 7, 9, 30, 0, 0, time.UTC)
	vaultPath := t.TempDir()
	config := appConfig{Obsidian: obsidianConfig{
		Enabled:       true,
		VaultPath:     vaultPath,
		DailyNotePath: "Daily Notes/{date}.md",
	}}
	day := normalizeDay(now)
	notePath := filepath.Join(vaultPath, "Daily Notes", "2026-05-07.md")
	if err := os.MkdirAll(filepath.Dir(notePath), 0o700); err != nil {
		t.Fatalf("make note directory: %v", err)
	}
	if err := os.WriteFile(notePath, []byte("# Daily note\n\n## TODO\n- [ ] retain importance\n- [x] finished\n- [~] underway\n- [-] abandoned\n\n## Notes\nKeep this text.\n"), 0o600); err != nil {
		t.Fatalf("write note: %v", err)
	}
	st := store{Todos: map[string][]todoItem{
		dateKey(day): {{Text: "retain importance", Important: true, CreatedAt: now.Add(-time.Hour)}},
	}}

	changed, err := syncDayFromObsidian(&st, day, config, now)
	if err != nil {
		t.Fatalf("read sync: %v", err)
	}
	if !changed {
		t.Fatal("expected note TODO section to be imported")
	}
	items := st.Todos[dateKey(day)]
	if len(items) != 4 || !items[0].Important || !items[0].CreatedAt.Equal(now.Add(-time.Hour)) || !items[1].Pending || !items[2].Completed || !items[3].Cancelled {
		t.Fatalf("imported items = %#v", items)
	}

	items = append(items, todoItem{Text: "new task", CreatedAt: now})
	setStoreItemsForDay(&st, day, items)
	if err := syncDayToObsidian(st, day, config); err != nil {
		t.Fatalf("write sync: %v", err)
	}
	data, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatalf("read note: %v", err)
	}
	want := "# Daily note\n\n## TODO\n- [ ] retain importance\n- [~] underway\n- [ ] new task\n- [x] finished\n- [-] abandoned\n## Notes\nKeep this text.\n"
	if string(data) != want {
		t.Fatalf("note = %q, want %q", data, want)
	}
}

func TestRolloverIncompleteTodos(t *testing.T) {
	now := time.Date(2026, time.May, 7, 9, 30, 0, 0, time.UTC)
	target := []todoItem{{Text: "today existing", CreatedAt: now.Add(-time.Hour)}}
	source := []todoItem{
		{Text: "carry pending", Pending: true, CreatedAt: now.Add(-48 * time.Hour)},
		{Text: "carry open", CreatedAt: now.Add(-48 * time.Hour)},
		{Text: "done", Completed: true, DoneAt: now.Add(-47 * time.Hour)},
		{Text: "cancelled", Cancelled: true, DoneAt: now.Add(-47 * time.Hour)},
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
