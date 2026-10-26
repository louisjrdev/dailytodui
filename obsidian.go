package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const defaultObsidianDailyNotePath = "{date}.md"

type appConfig struct {
	Obsidian obsidianConfig `json:"obsidian"`
}

type obsidianConfig struct {
	Enabled       bool   `json:"enabled"`
	VaultPath     string `json:"vault_path"`
	DailyNotePath string `json:"daily_note_path,omitempty"`
}

func configFilePath(storageDir string) string {
	return filepath.Join(storageDir, "config.json")
}

func loadConfig(storageDir string) (appConfig, error) {
	data, err := os.ReadFile(configFilePath(storageDir))
	if errors.Is(err, os.ErrNotExist) {
		return appConfig{}, nil
	}
	if err != nil {
		return appConfig{}, err
	}

	var config appConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return appConfig{}, fmt.Errorf("parse config: %w", err)
	}
	return config, nil
}

func saveConfig(storageDir string, config appConfig) error {
	if err := os.MkdirAll(storageDir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomically(configFilePath(storageDir), data, 0o600)
}

func syncDayFromObsidian(st *store, day time.Time, config appConfig, now time.Time) (bool, error) {
	if !config.Obsidian.Enabled {
		return false, nil
	}

	path, err := obsidianDailyNotePath(config.Obsidian, day, false)
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	key := dateKey(day)
	previous := st.Todos[key]
	items, found := readObsidianTodos(string(data), previous, now)
	if !found {
		return false, nil
	}
	setStoreItemsForDay(st, day, items)
	return !slices.Equal(previous, st.Todos[key]), nil
}

func syncDayToObsidian(st store, day time.Time, config appConfig) error {
	if !config.Obsidian.Enabled {
		return nil
	}

	path, err := obsidianDailyNotePath(config.Obsidian, day, true)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		data = nil
	} else if err != nil {
		return err
	}

	updated := writeObsidianTodos(string(data), st.Todos[dateKey(day)])
	return writeFileAtomically(path, []byte(updated), 0o600)
}

func obsidianDailyNotePath(config obsidianConfig, day time.Time, create bool) (string, error) {
	vaultPath := strings.TrimSpace(config.VaultPath)
	if vaultPath == "" {
		return "", errors.New("Obsidian sync is enabled but obsidian.vault_path is empty")
	}

	vaultPath, err := filepath.Abs(vaultPath)
	if err != nil {
		return "", err
	}
	vaultPath, err = filepath.EvalSymlinks(vaultPath)
	if err != nil {
		return "", fmt.Errorf("resolve Obsidian vault: %w", err)
	}
	info, err := os.Stat(vaultPath)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("Obsidian vault path is not a directory: %s", vaultPath)
	}

	template := strings.TrimSpace(config.DailyNotePath)
	if template == "" {
		template = defaultObsidianDailyNotePath
	}
	if !strings.Contains(template, "{date}") {
		return "", errors.New("obsidian.daily_note_path must include {date}")
	}
	relativePath := filepath.Clean(strings.ReplaceAll(template, "{date}", dateKey(day)))
	if filepath.IsAbs(relativePath) || relativePath == "." || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
		return "", errors.New("obsidian.daily_note_path must stay within the vault")
	}

	path := filepath.Join(vaultPath, relativePath)
	if !isPathWithin(vaultPath, path) {
		return "", errors.New("Obsidian daily note path escapes the vault")
	}
	if create {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return "", err
		}
		resolvedDir, err := filepath.EvalSymlinks(filepath.Dir(path))
		if err != nil {
			return "", err
		}
		if !isPathWithin(vaultPath, resolvedDir) {
			return "", errors.New("Obsidian daily note directory escapes the vault")
		}
		path = filepath.Join(resolvedDir, filepath.Base(path))
	}
	if resolvedPath, err := filepath.EvalSymlinks(path); err == nil && !isPathWithin(vaultPath, resolvedPath) {
		return "", errors.New("Obsidian daily note escapes the vault")
	}
	return path, nil
}

func isPathWithin(root, path string) bool {
	relativePath, err := filepath.Rel(root, path)
	return err == nil && relativePath != ".." && !strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) && !filepath.IsAbs(relativePath)
}

func readObsidianTodos(note string, existing []todoItem, now time.Time) ([]todoItem, bool) {
	lines := strings.Split(note, "\n")
	start, end, found := obsidianTODOSection(lines)
	if !found {
		return nil, false
	}

	items := make([]todoItem, 0, end-start)
	used := make([]bool, len(existing))
	for _, line := range lines[start:end] {
		item, ok := parseObsidianTodo(line, now)
		if !ok {
			continue
		}
		for i, previous := range existing {
			if !used[i] && previous.Text == item.Text {
				item.CreatedAt = previous.CreatedAt
				item.Important = previous.Important
				if (item.Completed || item.Cancelled) && !previous.DoneAt.IsZero() {
					item.DoneAt = previous.DoneAt
				}
				used[i] = true
				break
			}
		}
		items = append(items, item)
	}
	return items, true
}

func parseObsidianTodo(line string, now time.Time) (todoItem, bool) {
	line = strings.TrimSpace(line)
	if len(line) < 6 || (line[0] != '-' && line[0] != '*' && line[0] != '+') || line[1] != ' ' || line[2] != '[' || line[4] != ']' || line[5] != ' ' {
		return todoItem{}, false
	}

	text := strings.TrimSpace(line[6:])
	if text == "" {
		return todoItem{}, false
	}
	item := todoItem{Text: text, CreatedAt: now}
	switch line[3] {
	case 'x', 'X':
		item.Completed = true
		item.DoneAt = now
	case '~', '/':
		item.Pending = true
	case '-':
		item.Cancelled = true
		item.DoneAt = now
	case ' ':
	default:
		return todoItem{}, false
	}
	return item, true
}

func writeObsidianTodos(note string, items []todoItem) string {
	lines := strings.Split(note, "\n")
	start, end, found := obsidianTODOSection(lines)
	todoLines := make([]string, 0, len(items))
	for _, item := range items {
		todoLines = append(todoLines, formatObsidianTodo(item))
	}

	if !found {
		if len(lines) == 1 && lines[0] == "" {
			return strings.Join(append([]string{"## TODO"}, todoLines...), "\n") + "\n"
		}
		if lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		return strings.Join(append(append(lines, "", "## TODO"), todoLines...), "\n") + "\n"
	}

	updated := make([]string, 0, len(lines)-end+start+len(todoLines))
	updated = append(updated, lines[:start]...)
	updated = append(updated, todoLines...)
	updated = append(updated, lines[end:]...)
	return strings.Join(updated, "\n")
}

func obsidianTODOSection(lines []string) (int, int, bool) {
	for i, line := range lines {
		if strings.TrimSpace(line) != "## TODO" {
			continue
		}
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			trimmed := strings.TrimLeft(lines[j], " \t")
			if strings.HasPrefix(trimmed, "# ") || strings.HasPrefix(trimmed, "## ") {
				end = j
				break
			}
		}
		return i + 1, end, true
	}
	return 0, 0, false
}

func formatObsidianTodo(item todoItem) string {
	box := " "
	if item.Completed {
		box = "x"
	} else if item.Pending {
		box = "~"
	} else if item.Cancelled {
		box = "-"
	}
	text := strings.Join(strings.Fields(strings.ReplaceAll(item.Text, "\r", "")), " ")
	return "- [" + box + "] " + text
}

func writeFileAtomically(path string, data []byte, mode os.FileMode) error {
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporaryFile, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	temporaryPath := temporaryFile.Name()
	defer os.Remove(temporaryPath)
	if err := temporaryFile.Chmod(mode); err != nil {
		temporaryFile.Close()
		return err
	}
	if _, err := temporaryFile.Write(data); err != nil {
		temporaryFile.Close()
		return err
	}
	if err := temporaryFile.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
