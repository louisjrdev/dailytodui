package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const dayLayout = "2006-01-02"

type inputMode int

const (
	modeNormal inputMode = iota
	modeAdd
	modeEdit
	modeJump
)

type todoItem struct {
	Text      string    `json:"text"`
	Pending   bool      `json:"pending,omitempty"`
	Completed bool      `json:"completed"`
	CreatedAt time.Time `json:"created_at"`
	DoneAt    time.Time `json:"done_at,omitempty"`
}

type store struct {
	Todos map[string][]todoItem `json:"todos"`
}

type model struct {
	store      store
	day        time.Time
	cursor     int
	mode       inputMode
	input      textinput.Model
	storageDir string
	width      int
	height     int
	status     string
	err        error
}

func main() {
	today := normalizeDay(time.Now())
	storageDir, err := defaultStorageDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "storage path error: %v\n", err)
		os.Exit(1)
	}

	st, err := loadStore(storageDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load error: %v\n", err)
		os.Exit(1)
	}

	input := textinput.New()
	input.CharLimit = 280
	input.Prompt = "> "

	m := model{
		store:      st,
		day:        today,
		input:      input,
		storageDir: storageDir,
		status:     "Left/right switch days. a add. e edit. d delete. r roll over. space toggle. g jump.",
	}
	m.clampCursor()

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "app error: %v\n", err)
		os.Exit(1)
	}
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.input.Width = max(20, msg.Width-8)
		return m, nil
	case tea.KeyMsg:
		if m.mode != modeNormal {
			return m.updateInputMode(msg)
		}
		return m.updateNormalMode(msg)
	}
	return m, nil
}

func (m model) updateInputMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		m.input.Blur()
		m.input.SetValue("")
		m.status = "Cancelled."
		return m, nil
	case "enter":
		value := strings.TrimSpace(m.input.Value())
		m.input.SetValue("")
		m.input.Blur()
		switch m.mode {
		case modeAdd:
			m.mode = modeNormal
			if value == "" {
				m.status = "Empty todo ignored."
				return m, nil
			}
			items := append(m.itemsForDay(), todoItem{Text: value, CreatedAt: time.Now()})
			m.setItemsForDay(items)
			m.cursor = len(items) - 1
			m.status = "Todo added."
			m.persist()
			return m, nil
		case modeEdit:
			m.mode = modeNormal
			items := m.itemsForDay()
			if len(items) == 0 || m.cursor >= len(items) {
				m.status = "No todo selected."
				return m, nil
			}
			if value == "" {
				m.status = "Empty todo ignored."
				return m, nil
			}
			items[m.cursor].Text = value
			m.setItemsForDay(items)
			m.status = "Todo updated."
			m.persist()
			return m, nil
		case modeJump:
			m.mode = modeNormal
			day, err := parseDayInput(value, time.Now())
			if err != nil {
				m.status = "Use YYYY-MM-DD."
				m.err = err
				return m, nil
			}
			m.err = nil
			m.day = day
			m.clampCursor()
			m.status = fmt.Sprintf("Jumped to %s.", m.day.Format(dayLayout))
			return m, nil
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m model) updateNormalMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "left", "h":
		m.day = m.day.AddDate(0, 0, -1)
		m.clampCursor()
		m.status = fmt.Sprintf("Viewing %s.", dayLabel(m.day))
	case "right", "l":
		m.day = m.day.AddDate(0, 0, 1)
		m.clampCursor()
		m.status = fmt.Sprintf("Viewing %s.", dayLabel(m.day))
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.itemsForDay())-1 {
			m.cursor++
		}
	case "shift+up", "K":
		m.moveSelected(-1)
	case "shift+down", "J":
		m.moveSelected(1)
	case "a":
		m.mode = modeAdd
		m.input.Placeholder = "new todo"
		m.input.SetValue("")
		m.input.Focus()
		m.status = "Add todo for current day. Enter saves."
	case "e":
		items := m.itemsForDay()
		if len(items) == 0 {
			m.status = "No todo selected."
			break
		}
		m.mode = modeEdit
		m.input.Placeholder = "edit todo"
		m.input.SetValue(items[m.cursor].Text)
		m.input.Focus()
		m.status = "Edit todo. Enter saves."
	case "d", "x":
		items := m.itemsForDay()
		if len(items) == 0 {
			m.status = "No todo selected."
			break
		}
		deleted := items[m.cursor].Text
		items = append(items[:m.cursor], items[m.cursor+1:]...)
		m.setItemsForDay(items)
		m.clampCursor()
		m.status = fmt.Sprintf("Deleted: %s", deleted)
		m.persist()
	case " ":
		items := m.itemsForDay()
		if len(items) == 0 {
			m.status = "No todo selected."
			break
		}
		selected := items[m.cursor]
		items[m.cursor].Completed = !items[m.cursor].Completed
		if items[m.cursor].Completed {
			items[m.cursor].Pending = false
			items[m.cursor].DoneAt = time.Now()
		} else {
			items[m.cursor].DoneAt = time.Time{}
		}
		selected = items[m.cursor]
		items = partitionCompleted(items)
		m.setItemsForDay(items)
		m.cursor = indexOfItem(items, selected)
		m.clampCursor()
		m.status = "Todo toggled."
		m.persist()
	case "p":
		items := m.itemsForDay()
		if len(items) == 0 {
			m.status = "No todo selected."
			break
		}
		selected := items[m.cursor]
		items[m.cursor].Pending = !items[m.cursor].Pending
		if items[m.cursor].Pending {
			items[m.cursor].Completed = false
			items[m.cursor].DoneAt = time.Time{}
		}
		selected = items[m.cursor]
		items = partitionCompleted(items)
		m.setItemsForDay(items)
		m.cursor = indexOfItem(items, selected)
		m.clampCursor()
		m.status = "In-progress toggled."
		m.persist()
	case "g":
		m.mode = modeJump
		m.input.Placeholder = "YYYY-MM-DD"
		m.input.SetValue(m.day.Format(dayLayout))
		m.input.Focus()
		m.status = "Jump to date."
	case "t":
		m.day = normalizeDay(time.Now())
		m.clampCursor()
		m.status = "Back to today."
	case "r":
		previousDay, ok := lastNonEmptyDayBefore(m.store.Todos, m.day)
		if !ok {
			m.status = "No earlier non-empty day found."
			break
		}

		items, added := rolloverIncompleteTodos(m.itemsForDay(), m.store.Todos[dateKey(previousDay)], time.Now())
		if added == 0 {
			m.status = fmt.Sprintf("No incomplete todos to roll over from %s.", previousDay.Format(dayLayout))
			break
		}

		m.setItemsForDay(items)
		m.cursor = min(m.cursor, len(items)-1)
		m.clampCursor()
		if added == 1 {
			m.status = fmt.Sprintf("Rolled over 1 todo from %s.", previousDay.Format(dayLayout))
		} else {
			m.status = fmt.Sprintf("Rolled over %d todos from %s.", added, previousDay.Format(dayLayout))
		}
		m.persist()
	}
	return m, nil
}

func (m *model) moveSelected(delta int) {
	items := m.itemsForDay()
	if len(items) == 0 {
		m.status = "No todo selected."
		return
	}
	updated, next := moveItemWithinSection(items, m.cursor, delta)
	if next == m.cursor {
		m.status = "Cannot move farther."
		return
	}
	m.setItemsForDay(updated)
	m.cursor = next
	m.status = "Todo moved."
	m.persist()
}

func (m *model) persist() {
	if err := saveStore(m.storageDir, m.store); err != nil {
		m.err = err
		m.status = fmt.Sprintf("Save failed: %v", err)
		return
	}
	m.err = nil
}

func (m *model) itemsForDay() []todoItem {
	items := m.store.Todos[dateKey(m.day)]
	return append([]todoItem(nil), items...)
}

func (m *model) setItemsForDay(items []todoItem) {
	if m.store.Todos == nil {
		m.store.Todos = map[string][]todoItem{}
	}
	key := dateKey(m.day)
	items = partitionCompleted(items)
	if len(items) == 0 {
		delete(m.store.Todos, key)
		return
	}
	m.store.Todos[key] = items
}

func (m *model) clampCursor() {
	items := m.store.Todos[dateKey(m.day)]
	if len(items) == 0 {
		m.cursor = 0
		return
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(items) {
		m.cursor = len(items) - 1
	}
}

func (m model) View() string {
	title := lipgloss.NewStyle().Bold(true).Render(dayLabel(m.day))
	dateLine := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(m.day.Format("Monday, 02 Jan 2006"))
	header := strings.Join([]string{title, dateLine, ""}, "\n")

	items := m.store.Todos[dateKey(m.day)]
	var lines []string
	if len(items) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("No todos for this day. Press a to add one."))
	} else {
		for i, item := range items {
			cursor := "  "
			if i == m.cursor {
				cursor = "> "
			}
			box := "[ ]"
			text := item.Text
			if item.Completed {
				box = "[x]"
				text = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Strikethrough(true).Render(text)
			} else if item.Pending {
				box = "[~]"
				text = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(text)
			}
			lines = append(lines, cursor+box+" "+text)
		}
	}

	help := lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Render("h/left prev  l/right next  j/k move  shift+up/down reorder  a add  e edit  d delete  r roll over  p in-progress  space complete  g goto  t today  q quit")

	body := strings.Join(lines, "\n")
	if m.mode == modeAdd || m.mode == modeEdit || m.mode == modeJump {
		prompt := "Input"
		switch m.mode {
		case modeAdd:
			prompt = "Add"
		case modeEdit:
			prompt = "Edit"
		case modeJump:
			prompt = "Goto"
		}
		body += "\n\n" + lipgloss.NewStyle().Bold(true).Render(prompt+": ") + m.input.View()
	}

	footer := "\n" + help
	if m.height <= 0 {
		return strings.Join([]string{header, body, footer}, "\n")
	}

	bodyHeight := max(0, m.height-lipgloss.Height(header)-lipgloss.Height(footer))
	if bodyHeight > 0 {
		body = lipgloss.NewStyle().Height(bodyHeight).Render(body)
	}

	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

func parseDayInput(value string, now time.Time) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return normalizeDay(now), nil
	}
	parsed, err := time.ParseInLocation(dayLayout, value, now.Location())
	if err != nil {
		return time.Time{}, err
	}
	return normalizeDay(parsed), nil
}

func normalizeDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

func dateKey(t time.Time) string {
	return normalizeDay(t).Format(dayLayout)
}

func dayLabel(day time.Time) string {
	today := normalizeDay(time.Now().In(day.Location()))
	days := int(day.Sub(today).Hours() / 24)
	switch days {
	case 0:
		return "Today"
	case 1:
		return "Tomorrow"
	case -1:
		return "Yesterday"
	default:
		return day.Format(dayLayout)
	}
}

func lastNonEmptyDayBefore(todos map[string][]todoItem, before time.Time) (time.Time, bool) {
	var latest time.Time
	found := false
	for key, items := range todos {
		if len(items) == 0 {
			continue
		}
		day, err := time.ParseInLocation(dayLayout, key, before.Location())
		if err != nil {
			continue
		}
		day = normalizeDay(day)
		if !day.Before(before) {
			continue
		}
		if !found || day.After(latest) {
			latest = day
			found = true
		}
	}
	return latest, found
}

func rolloverIncompleteTodos(target, source []todoItem, now time.Time) ([]todoItem, int) {
	rolled := append([]todoItem(nil), target...)
	existing := map[string]struct{}{}
	for _, item := range target {
		existing[item.Text] = struct{}{}
	}

	added := 0
	for _, item := range source {
		if item.Completed {
			continue
		}
		if _, ok := existing[item.Text]; ok {
			continue
		}
		copy := item
		copy.CreatedAt = now
		copy.Completed = false
		copy.DoneAt = time.Time{}
		rolled = append(rolled, copy)
		existing[copy.Text] = struct{}{}
		added++
	}

	return partitionCompleted(rolled), added
}

func partitionCompleted(items []todoItem) []todoItem {
	if len(items) < 2 {
		return append([]todoItem(nil), items...)
	}
	pending := make([]todoItem, 0, len(items))
	open := make([]todoItem, 0, len(items))
	done := make([]todoItem, 0, len(items))
	for _, item := range items {
		if item.Completed {
			done = append(done, item)
			continue
		}
		if item.Pending {
			pending = append(pending, item)
			continue
		}
		open = append(open, item)
	}
	return append(append(pending, open...), done...)
}

func moveItemWithinSection(items []todoItem, idx, delta int) ([]todoItem, int) {
	if idx < 0 || idx >= len(items) || delta == 0 {
		return items, idx
	}
	sectionStart, sectionEnd := sectionBounds(items, idx)
	next := idx + delta
	if next < sectionStart || next > sectionEnd {
		return items, idx
	}
	updated := append([]todoItem(nil), items...)
	updated[idx], updated[next] = updated[next], updated[idx]
	return updated, next
}

func sectionBounds(items []todoItem, idx int) (int, int) {
	status := itemStatus(items[idx])
	start, end := idx, idx
	for start > 0 && itemStatus(items[start-1]) == status {
		start--
	}
	for end < len(items)-1 && itemStatus(items[end+1]) == status {
		end++
	}
	return start, end
}

func itemStatus(item todoItem) string {
	if item.Completed {
		return "completed"
	}
	if item.Pending {
		return "pending"
	}
	return "open"
}

func indexOfItem(items []todoItem, target todoItem) int {
	for i, item := range items {
		if item.Text == target.Text && item.Completed == target.Completed && item.CreatedAt.Equal(target.CreatedAt) {
			return i
		}
	}
	return 0
}

func defaultStorageDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "dailytodui"), nil
}

func loadStore(storageDir string) (store, error) {
	if err := os.MkdirAll(storageDir, 0o700); err != nil {
		return store{}, err
	}
	path := filepath.Join(storageDir, "todos.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store{Todos: map[string][]todoItem{}}, nil
	}
	if err != nil {
		return store{}, err
	}
	var st store
	if err := json.Unmarshal(data, &st); err != nil {
		return store{}, err
	}
	if st.Todos == nil {
		st.Todos = map[string][]todoItem{}
	}
	for key, items := range st.Todos {
		st.Todos[key] = partitionCompleted(items)
	}
	return st, nil
}

func saveStore(storageDir string, st store) error {
	if err := os.MkdirAll(storageDir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(storageDir, "todos.json")
	tmpPath := path + ".tmp"
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
