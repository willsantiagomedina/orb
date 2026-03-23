package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/willdev/orb/internal/tui/theme"
)

type zoneLayout struct {
	Width         int
	Height        int
	HeaderHeight  int
	ContentHeight int
	InputHeight   int
}

func computeZoneLayout(width int, height int, headerHeight int, inputHeight int) zoneLayout {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	if headerHeight < 1 {
		headerHeight = 1
	}
	if inputHeight < 1 {
		inputHeight = 1
	}

	contentHeight := height - headerHeight - inputHeight
	if contentHeight < 1 {
		contentHeight = 1
	}

	return zoneLayout{
		Width:         width,
		Height:        height,
		HeaderHeight:  headerHeight,
		ContentHeight: contentHeight,
		InputHeight:   inputHeight,
	}
}

func renderShell(width int, height int, header string, content string, input string) string {
	if width <= 0 || height <= 0 {
		return ""
	}

	headerHeight := maxInt(1, lipgloss.Height(header))
	inputHeight := maxInt(1, lipgloss.Height(input))
	layout := computeZoneLayout(width, height, headerHeight, inputHeight)

	renderedHeader := fillZone(layout.Width, layout.HeaderHeight, theme.BG2, header)
	renderedContent := fillZone(layout.Width, layout.ContentHeight, theme.BG0, content)
	renderedInput := fillZone(layout.Width, layout.InputHeight, theme.BG1, input)

	body := lipgloss.JoinVertical(lipgloss.Left, renderedHeader, renderedContent, renderedInput)
	return fillZone(width, height, theme.BG0, body)
}

func fillZone(width int, height int, background lipgloss.TerminalColor, content string) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	clean := strings.ReplaceAll(content, "\r", "")
	return lipgloss.Place(
		width,
		height,
		lipgloss.Left,
		lipgloss.Top,
		clean,
		lipgloss.WithWhitespaceChars(" "),
		lipgloss.WithWhitespaceBackground(background),
	)
}

func clampBlock(content string, width int, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}

	lines := strings.Split(strings.ReplaceAll(content, "\r", ""), "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}

	for i := range lines {
		lines[i] = truncateAndPadANSI(lines[i], width)
	}

	return strings.Join(lines, "\n")
}

func truncateAndPadANSI(input string, width int) string {
	if width <= 0 {
		return ""
	}
	clean := strings.ReplaceAll(input, "\t", "    ")
	truncated := ansi.Truncate(clean, width, "")
	visible := ansi.StringWidth(truncated)
	if visible < width {
		truncated += strings.Repeat(" ", width-visible)
	}
	return truncated
}
