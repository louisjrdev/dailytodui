package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
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
	Cancelled bool      `json:"cancelled,omitempty"`
	Important bool      `json:"important,omitempty"`
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
	config     appConfig
	width      int
	height     int
	status     string
	err        error
}

func main() {
	if len(os.Args) > 1 && os.Args[1] != "tui" {
		os.Exit(runCLI(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, time.Now()))
	}

	launchTUI()
}

func launchTUI() {
	today := normalizeDay(time.Now())
	storageDir, err := defaultStorageDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "storage path error: %v\n", err)
		os.Exit(1)
	}

	config, err := loadConfig(storageDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}
	st, err := loadStore(storageDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load error: %v\n", err)
		os.Exit(1)
	}
	if changed, err := syncDayFromObsidian(&st, today, config, time.Now()); err != nil {
		fmt.Fprintf(os.Stderr, "Obsidian sync error: %v\n", err)
		os.Exit(1)
	} else if changed {
		if err := saveStore(storageDir, st); err != nil {
			fmt.Fprintf(os.Stderr, "save error: %v\n", err)
			os.Exit(1)
		}
	}

	input := textinput.New()
	input.CharLimit = 280
	input.Prompt = "> "

	m := model{
		store:      st,
		day:        today,
		input:      input,
		storageDir: storageDir,
		config:     config,
		status:     "Left/right switch days. a add. e edit. d delete. i important. r roll over. p in-progress. c cancel. space complete. g jump.",
	}
	m.clampCursor()

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "app error: %v\n", err)
		os.Exit(1)
	}
}

func runCLI(args []string, stdin io.Reader, stdout, stderr io.Writer, now time.Time) int {
	if len(args) == 0 {
		printCLIUsage(stderr)
		return 2
	}

	switch args[0] {
	case "add":
		return runAddCommand(args[1:], stdin, stdout, stderr, now)
	case "complete", "done":
		return runCompleteCommand(args[1:], stdout, stderr, now)
	case "status":
		return runStatusCommand(args[1:], stdout, stderr, now)
	case "list", "ls":
		return runListCommand(args[1:], stdout, stderr, now)
	case "config":
		return runConfigCommand(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printCLIUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
		printCLIUsage(stderr)
		return 2
	}
}

type repeatStringFlag []string

func (f *repeatStringFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *repeatStringFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("todo text cannot be empty")
	}
	*f = append(*f, value)
	return nil
}

func runAddCommand(args []string, stdin io.Reader, stdout, stderr io.Writer, now time.Time) int {
	fs := cliFlagSet("add", stderr)
	var storageDir string
	var dayValue string
	var statusValue string
	var todos repeatStringFlag
	var readStdin bool
	fs.StringVar(&storageDir, "storage-dir", "", "storage directory (defaults to user config dir)")
	fs.StringVar(&dayValue, "day", "", "todo day (YYYY-MM-DD, today, tomorrow, yesterday)")
	fs.StringVar(&statusValue, "status", "open", "initial status: open, pending, completed, cancelled")
	fs.Var(&todos, "todo", "todo text; may be repeated")
	fs.BoolVar(&readStdin, "stdin", false, "read newline-separated todos from stdin")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	texts := append([]string(nil), todos...)
	if fs.NArg() > 0 {
		text := strings.TrimSpace(strings.Join(fs.Args(), " "))
		if text != "" {
			texts = append(texts, text)
		}
	}
	if readStdin {
		data, err := io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintf(stderr, "read stdin error: %v\n", err)
			return 1
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				texts = append(texts, line)
			}
		}
	}
	if len(texts) == 0 {
		fmt.Fprintln(stderr, "add requires todo text, --todo, or --stdin")
		return 2
	}

	day, err := parseCLIDay(dayValue, now)
	if err != nil {
		fmt.Fprintf(stderr, "day error: %v\n", err)
		return 2
	}
	statusValue, err = normalizeCLIStatus(statusValue)
	if err != nil {
		fmt.Fprintf(stderr, "status error: %v\n", err)
		return 2
	}
	storageDir, err = cliStorageDir(storageDir)
	if err != nil {
		fmt.Fprintf(stderr, "storage path error: %v\n", err)
		return 1
	}
	st, config, err := loadSyncedStore(storageDir, day, now)
	if err != nil {
		fmt.Fprintf(stderr, "load error: %v\n", err)
		return 1
	}

	items := append([]todoItem(nil), st.Todos[dateKey(day)]...)
	for _, text := range texts {
		item := todoItem{Text: text, CreatedAt: now}
		applyCLIStatus(&item, statusValue, now)
		items = append(items, item)
	}
	setStoreItemsForDay(&st, day, items)
	if err := saveSyncedStore(storageDir, st, day, config); err != nil {
		fmt.Fprintf(stderr, "save error: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "Added %d todo(s) for %s.\n", len(texts), dateKey(day))
	return 0
}

func runCompleteCommand(args []string, stdout, stderr io.Writer, now time.Time) int {
	fs := cliFlagSet("complete", stderr)
	var storageDir string
	var dayValue string
	var index int
	fs.StringVar(&storageDir, "storage-dir", "", "storage directory (defaults to user config dir)")
	fs.StringVar(&dayValue, "day", "", "todo day (YYYY-MM-DD, today, tomorrow, yesterday)")
	fs.IntVar(&index, "index", 0, "1-based todo index from list command")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	return runSetStatus(storageDir, dayValue, index, strings.Join(fs.Args(), " "), "completed", stdout, stderr, now)
}

func runStatusCommand(args []string, stdout, stderr io.Writer, now time.Time) int {
	fs := cliFlagSet("status", stderr)
	var storageDir string
	var dayValue string
	var index int
	fs.StringVar(&storageDir, "storage-dir", "", "storage directory (defaults to user config dir)")
	fs.StringVar(&dayValue, "day", "", "todo day (YYYY-MM-DD, today, tomorrow, yesterday)")
	fs.IntVar(&index, "index", 0, "1-based todo index from list command")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(stderr, "status requires one of: open, pending, completed, cancelled")
		return 2
	}
	statusValue, err := normalizeCLIStatus(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "status error: %v\n", err)
		return 2
	}
	return runSetStatus(storageDir, dayValue, index, strings.Join(fs.Args()[1:], " "), statusValue, stdout, stderr, now)
}

func runSetStatus(storageDir, dayValue string, index int, text, statusValue string, stdout, stderr io.Writer, now time.Time) int {
	day, err := parseCLIDay(dayValue, now)
	if err != nil {
		fmt.Fprintf(stderr, "day error: %v\n", err)
		return 2
	}
	storageDir, err = cliStorageDir(storageDir)
	if err != nil {
		fmt.Fprintf(stderr, "storage path error: %v\n", err)
		return 1
	}
	st, config, err := loadSyncedStore(storageDir, day, now)
	if err != nil {
		fmt.Fprintf(stderr, "load error: %v\n", err)
		return 1
	}

	updated, err := setTodoStatus(&st, day, index, text, statusValue, now)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 2
	}
	if err := saveSyncedStore(storageDir, st, day, config); err != nil {
		fmt.Fprintf(stderr, "save error: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "Set %q to %s for %s.\n", updated.Text, statusValue, dateKey(day))
	return 0
}

func runListCommand(args []string, stdout, stderr io.Writer, now time.Time) int {
	fs := cliFlagSet("list", stderr)
	var storageDir string
	var dayValue string
	var jsonOutput bool
	fs.StringVar(&storageDir, "storage-dir", "", "storage directory (defaults to user config dir)")
	fs.StringVar(&dayValue, "day", "", "todo day (YYYY-MM-DD, today, tomorrow, yesterday)")
	fs.BoolVar(&jsonOutput, "json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	day, err := parseCLIDay(dayValue, now)
	if err != nil {
		fmt.Fprintf(stderr, "day error: %v\n", err)
		return 2
	}
	storageDir, err = cliStorageDir(storageDir)
	if err != nil {
		fmt.Fprintf(stderr, "storage path error: %v\n", err)
		return 1
	}
	st, _, err := loadSyncedStore(storageDir, day, now)
	if err != nil {
		fmt.Fprintf(stderr, "load error: %v\n", err)
		return 1
	}

	items := st.Todos[dateKey(day)]
	if jsonOutput {
		payload := cliListPayload{Date: dateKey(day), Todos: make([]cliListItem, 0, len(items))}
		for i, item := range items {
			var doneAt *time.Time
			if !item.DoneAt.IsZero() {
				doneAt = &item.DoneAt
			}
			payload.Todos = append(payload.Todos, cliListItem{
				Index:     i + 1,
				Text:      item.Text,
				Status:    itemStatus(item),
				Important: item.Important,
				CreatedAt: item.CreatedAt,
				DoneAt:    doneAt,
			})
		}
		if err := json.NewEncoder(stdout).Encode(payload); err != nil {
			fmt.Fprintf(stderr, "json error: %v\n", err)
			return 1
		}
		return 0
	}

	fmt.Fprintf(stdout, "%s\n", dateKey(day))
	if len(items) == 0 {
		fmt.Fprintln(stdout, "No todos.")
		return 0
	}
	for i, item := range items {
		box := "[ ]"
		if item.Completed {
			box = "[x]"
		} else if item.Pending {
			box = "[~]"
		} else if item.Cancelled {
			box = "[-]"
		}
		fmt.Fprintf(stdout, "%d. %s %s\n", i+1, box, item.Text)
	}
	return 0
}

type cliListPayload struct {
	Date  string        `json:"date"`
	Todos []cliListItem `json:"todos"`
}

type cliListItem struct {
	Index     int        `json:"index"`
	Text      string     `json:"text"`
	Status    string     `json:"status"`
	Important bool       `json:"important"`
	CreatedAt time.Time  `json:"created_at"`
	DoneAt    *time.Time `json:"done_at,omitempty"`
}

func cliFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

func printCLIUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage:
  dailytodui                      launch the TUI
  dailytodui tui                  launch the TUI
  dailytodui add [flags] TEXT     add a todo
  dailytodui complete [flags] TEXT
  dailytodui status [flags] STATUS TEXT
  dailytodui list [flags]
  dailytodui config [flags]

Commands:
  add       Add one or more todos. Use --todo multiple times or --stdin for bulk adds.
  complete  Mark a todo completed by exact text or --index.
  status    Set a todo status: open, pending, completed, cancelled.
  list      List todos for a day, with indexes for --index.
  config    View or update configuration. See dailytodui config --help.

Common flags:
  --day YYYY-MM-DD|today|tomorrow|yesterday
  --storage-dir PATH`)
}

func cliStorageDir(value string) (string, error) {
	if strings.TrimSpace(value) != "" {
		return value, nil
	}
	return defaultStorageDir()
}

func runConfigCommand(args []string, stdout, stderr io.Writer) int {
	fs := cliFlagSet("config", stderr)
	var storageDir string
	var obsidianSync bool
	var vaultPath string
	var dailyNotePath string
	fs.StringVar(&storageDir, "storage-dir", "", "storage directory (defaults to user config dir)")
	fs.BoolVar(&obsidianSync, "obsidian-sync", false, "enable or disable Obsidian daily-note sync")
	fs.StringVar(&vaultPath, "obsidian-vault", "", "path to the Obsidian vault")
	fs.StringVar(&dailyNotePath, "obsidian-daily-note", "", "vault-relative note template containing {date}")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "config does not accept positional arguments")
		return 2
	}
	storageDir, err := cliStorageDir(storageDir)
	if err != nil {
		fmt.Fprintf(stderr, "storage path error: %v\n", err)
		return 1
	}
	config, err := loadConfig(storageDir)
	if err != nil {
		fmt.Fprintf(stderr, "config error: %v\n", err)
		return 1
	}

	updated := false
	fs.Visit(func(flag *flag.Flag) {
		switch flag.Name {
		case "obsidian-sync":
			config.Obsidian.Enabled = obsidianSync
			updated = true
		case "obsidian-vault":
			config.Obsidian.VaultPath = vaultPath
			updated = true
		case "obsidian-daily-note":
			config.Obsidian.DailyNotePath = dailyNotePath
			updated = true
		}
	})
	if config.Obsidian.Enabled && strings.TrimSpace(config.Obsidian.VaultPath) == "" {
		fmt.Fprintln(stderr, "config error: --obsidian-vault is required while Obsidian sync is enabled")
		return 2
	}
	if config.Obsidian.DailyNotePath != "" && !strings.Contains(config.Obsidian.DailyNotePath, "{date}") {
		fmt.Fprintln(stderr, "config error: --obsidian-daily-note must include {date}")
		return 2
	}
	if updated {
		if err := saveConfig(storageDir, config); err != nil {
			fmt.Fprintf(stderr, "config save error: %v\n", err)
			return 1
		}
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "config encode error: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, string(data))
	return 0
}

func loadSyncedStore(storageDir string, day time.Time, now time.Time) (store, appConfig, error) {
	config, err := loadConfig(storageDir)
	if err != nil {
		return store{}, appConfig{}, err
	}
	st, err := loadStore(storageDir)
	if err != nil {
		return store{}, appConfig{}, err
	}
	changed, err := syncDayFromObsidian(&st, day, config, now)
	if err != nil {
		return store{}, appConfig{}, fmt.Errorf("Obsidian sync: %w", err)
	}
	if changed {
		if err := saveStore(storageDir, st); err != nil {
			return store{}, appConfig{}, err
		}
	}
	return st, config, nil
}

func saveSyncedStore(storageDir string, st store, day time.Time, config appConfig) error {
	if err := saveStore(storageDir, st); err != nil {
		return err
	}
	if err := syncDayToObsidian(st, day, config); err != nil {
		return fmt.Errorf("Obsidian sync: %w", err)
	}
	return nil
}

func parseCLIDay(value string, now time.Time) (time.Time, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", "today":
		return normalizeDay(now), nil
	case "tomorrow":
		return normalizeDay(now.AddDate(0, 0, 1)), nil
	case "yesterday":
		return normalizeDay(now.AddDate(0, 0, -1)), nil
	default:
		return parseDayInput(value, now)
	}
}

func normalizeCLIStatus(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "open", "todo", "incomplete":
		return "open", nil
	case "pending", "in-progress", "progress":
		return "pending", nil
	case "completed", "complete", "done":
		return "completed", nil
	case "cancelled", "canceled", "cancel":
		return "cancelled", nil
	default:
		return "", fmt.Errorf("unknown status %q", value)
	}
}

func applyCLIStatus(item *todoItem, statusValue string, now time.Time) {
	switch statusValue {
	case "completed":
		item.Pending = false
		item.Completed = true
		item.Cancelled = false
		if item.DoneAt.IsZero() {
			item.DoneAt = now
		}
	case "cancelled":
		item.Pending = false
		item.Completed = false
		item.Cancelled = true
		if item.DoneAt.IsZero() {
			item.DoneAt = now
		}
	case "pending":
		item.Pending = true
		item.Completed = false
		item.Cancelled = false
		item.DoneAt = time.Time{}
	default:
		item.Pending = false
		item.Completed = false
		item.Cancelled = false
		item.DoneAt = time.Time{}
	}
}

func setTodoStatus(st *store, day time.Time, index int, text, statusValue string, now time.Time) (todoItem, error) {
	key := dateKey(day)
	items := append([]todoItem(nil), st.Todos[key]...)
	if len(items) == 0 {
		return todoItem{}, fmt.Errorf("no todos for %s", key)
	}

	idx, err := findTodoIndex(items, index, text)
	if err != nil {
		return todoItem{}, err
	}
	applyCLIStatus(&items[idx], statusValue, now)
	updated := items[idx]
	setStoreItemsForDay(st, day, items)
	return updated, nil
}

func findTodoIndex(items []todoItem, index int, text string) (int, error) {
	if index < 0 {
		return 0, fmt.Errorf("index %d out of range", index)
	}
	if index > 0 {
		if index > len(items) {
			return 0, fmt.Errorf("index %d out of range", index)
		}
		return index - 1, nil
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return 0, errors.New("provide exact todo text or --index")
	}

	matches := []int{}
	for i, item := range items {
		if item.Text == text {
			matches = append(matches, i)
		}
	}
	switch len(matches) {
	case 0:
		return 0, fmt.Errorf("todo not found: %q", text)
	case 1:
		return matches[0], nil
	default:
		return 0, fmt.Errorf("multiple todos match %q; use --index", text)
	}
}

func setStoreItemsForDay(st *store, day time.Time, items []todoItem) {
	if st.Todos == nil {
		st.Todos = map[string][]todoItem{}
	}
	key := dateKey(day)
	items = partitionCompleted(items)
	if len(items) == 0 {
		delete(st.Todos, key)
		return
	}
	st.Todos[key] = items
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
			if !m.reloadStore() {
				return m, nil
			}
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
	key := msg.String()
	if key == "ctrl+c" || key == "q" {
		return m, tea.Quit
	}
	if !m.reloadStore() {
		return m, nil
	}

	switch key {
	case "left", "h":
		m.day = m.day.AddDate(0, 0, -1)
		if !m.reloadStore() {
			return m, nil
		}
		m.status = fmt.Sprintf("Viewing %s.", dayLabel(m.day))
	case "right", "l":
		m.day = m.day.AddDate(0, 0, 1)
		if !m.reloadStore() {
			return m, nil
		}
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
			items[m.cursor].Cancelled = false
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
			items[m.cursor].Cancelled = false
			items[m.cursor].DoneAt = time.Time{}
		}
		selected = items[m.cursor]
		items = partitionCompleted(items)
		m.setItemsForDay(items)
		m.cursor = indexOfItem(items, selected)
		m.clampCursor()
		m.status = "In-progress toggled."
		m.persist()
	case "c":
		items := m.itemsForDay()
		if len(items) == 0 {
			m.status = "No todo selected."
			break
		}
		selected := items[m.cursor]
		items[m.cursor].Cancelled = !items[m.cursor].Cancelled
		if items[m.cursor].Cancelled {
			items[m.cursor].Pending = false
			items[m.cursor].Completed = false
			items[m.cursor].DoneAt = time.Now()
		} else {
			items[m.cursor].DoneAt = time.Time{}
		}
		selected = items[m.cursor]
		items = partitionCompleted(items)
		m.setItemsForDay(items)
		m.cursor = indexOfItem(items, selected)
		m.clampCursor()
		m.status = "Todo cancelled toggled."
		m.persist()
	case "g":
		m.mode = modeJump
		m.input.Placeholder = "YYYY-MM-DD"
		m.input.SetValue(m.day.Format(dayLayout))
		m.input.Focus()
		m.status = "Jump to date."
	case "t":
		m.day = normalizeDay(time.Now())
		if !m.reloadStore() {
			return m, nil
		}
		m.status = "Back to today."
	case "i":
		items := m.itemsForDay()
		if len(items) == 0 {
			m.status = "No todo selected."
			break
		}
		selected := items[m.cursor]
		items[m.cursor].Important = !items[m.cursor].Important
		selected = items[m.cursor]
		items = partitionCompleted(items)
		m.setItemsForDay(items)
		m.cursor = indexOfItem(items, selected)
		m.clampCursor()
		m.status = "Todo importance toggled."
		m.persist()
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
	latest, err := loadStore(m.storageDir)
	if err != nil {
		m.err = err
		m.status = fmt.Sprintf("Save failed: %v", err)
		return
	}
	config, err := loadConfig(m.storageDir)
	if err != nil {
		m.err = err
		m.status = fmt.Sprintf("Save failed: %v", err)
		return
	}

	key := dateKey(m.day)
	if items, ok := m.store.Todos[key]; ok {
		latest.Todos[key] = items
	} else {
		delete(latest.Todos, key)
	}
	m.store = latest
	m.config = config

	if err := saveSyncedStore(m.storageDir, m.store, m.day, m.config); err != nil {
		m.err = err
		m.status = fmt.Sprintf("Save failed: %v", err)
		return
	}
	m.err = nil
}

func (m *model) reloadStore() bool {
	st, config, err := loadSyncedStore(m.storageDir, m.day, time.Now())
	if err != nil {
		m.err = err
		m.status = fmt.Sprintf("Reload failed: %v", err)
		return false
	}
	m.store = st
	m.config = config
	m.clampCursor()
	m.err = nil
	return true
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
			style := lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
			box := "[ ]"
			if item.Completed {
				style = style.Foreground(lipgloss.Color("10")).Strikethrough(true)
				box = "[x]"
			} else if item.Pending {
				style = style.Foreground(lipgloss.Color("11"))
				box = "[~]"
			} else if item.Cancelled {
				style = style.Foreground(lipgloss.Color("8"))
				box = "[-]"
			}
			if item.Important {
				style = style.Foreground(lipgloss.Color("9"))
			}
			lines = append(lines, cursor+style.Render(box+" "+item.Text))
		}
	}

	help := lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Render("h/left prev  l/right next  j/k move  shift+up/down reorder  a add  e edit  d delete  i important  r roll over  p in-progress  c cancel  space complete  g goto  t today  q quit")

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
		if item.Completed || item.Cancelled {
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
	important := make([]todoItem, 0, len(items))
	pending := make([]todoItem, 0, len(items))
	open := make([]todoItem, 0, len(items))
	completed := make([]todoItem, 0, len(items))
	cancelled := make([]todoItem, 0, len(items))
	for _, item := range items {
		if item.Important {
			important = append(important, item)
			continue
		}
		if item.Completed {
			completed = append(completed, item)
			continue
		}
		if item.Cancelled {
			cancelled = append(cancelled, item)
			continue
		}
		if item.Pending {
			pending = append(pending, item)
			continue
		}
		open = append(open, item)
	}
	return append(append(append(append(important, pending...), open...), completed...), cancelled...)
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
	section := itemSection(items[idx])
	start, end := idx, idx
	for start > 0 && itemSection(items[start-1]) == section {
		start--
	}
	for end < len(items)-1 && itemSection(items[end+1]) == section {
		end++
	}
	return start, end
}

func itemSection(item todoItem) string {
	if item.Important {
		return "important"
	}
	return itemStatus(item)
}

func itemStatus(item todoItem) string {
	if item.Completed {
		return "completed"
	}
	if item.Cancelled {
		return "cancelled"
	}
	if item.Pending {
		return "pending"
	}
	return "open"
}

func indexOfItem(items []todoItem, target todoItem) int {
	for i, item := range items {
		if item.Text == target.Text && item.Pending == target.Pending && item.Completed == target.Completed && item.Cancelled == target.Cancelled && item.Important == target.Important && item.CreatedAt.Equal(target.CreatedAt) {
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
