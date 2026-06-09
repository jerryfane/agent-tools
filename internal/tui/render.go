package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

var (
	sidebarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")).
			Background(lipgloss.Color("236")).
			Padding(1, 1)
	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("16")).
			Background(lipgloss.Color("42")).
			Bold(true).
			Padding(0, 1)
	sidebarItemStyle = lipgloss.NewStyle().
				Padding(0, 1)
	bodyStyle = lipgloss.NewStyle().
			Padding(1, 1)
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("39"))
	mutedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244"))
	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("203"))
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("81"))
)

func renderSidebar(selected, width, height int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("agent-tools"))
	b.WriteString("\n\n")
	for i, item := range pages {
		line := item.label
		if i == selected {
			b.WriteString(selectedStyle.Width(max(1, width-4)).Render(line))
		} else {
			b.WriteString(sidebarItemStyle.Width(max(1, width-4)).Render(line))
		}
		b.WriteByte('\n')
	}
	for strings.Count(b.String(), "\n") < max(0, height-2) {
		b.WriteByte('\n')
	}
	return sidebarStyle.Width(max(10, width-2)).Height(max(1, height-2)).Render(b.String())
}

func renderRows(rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for i, cell := range row {
			if i >= len(widths) {
				continue
			}
			if len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}
	var b strings.Builder
	for rowIndex, row := range rows {
		for i, cell := range row {
			if i >= len(widths) {
				continue
			}
			padded := padRight(cell, widths[i])
			if rowIndex == 0 {
				b.WriteString(headerStyle.Render(padded))
			} else {
				b.WriteString(padded)
			}
			if i < len(widths)-1 {
				b.WriteString("  ")
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func padRight(value string, width int) string {
	if len(value) >= width {
		return value
	}
	return value + strings.Repeat(" ", width-len(value))
}

func sidebarWidth(total int) int {
	if total < 70 {
		return 14
	}
	return 18
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func percent(value *float64) string {
	if value == nil {
		return "??%"
	}
	return fmt.Sprintf("%.0f%%", *value)
}

func timeValue(value *time.Time, location *time.Location, includeDate bool) string {
	if value == nil {
		return "unknown"
	}
	t := value.In(location)
	if includeDate {
		return t.Format("2006-01-02 15:04")
	}
	return t.Format("15:04 MST")
}

func location(name string) *time.Location {
	if name == "" || strings.EqualFold(name, "local") {
		return time.Local
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.Local
	}
	return loc
}

func shortPath(value string, limit int) string {
	if value == "" {
		return "unknown"
	}
	return truncateStart(value, limit)
}

func shortSession(value string) string {
	if len(value) <= 8 {
		return value
	}
	return value[:8]
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func truncate(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= limit {
		return value
	}
	if limit <= 1 {
		return value[:limit]
	}
	return value[:limit-1] + "..."
}

func truncateStart(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return "..." + value[len(value)-limit+3:]
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
