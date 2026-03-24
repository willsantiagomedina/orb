package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestHandleMouseDoesNotScrollTextareaWhenPointerInsideInputField(t *testing.T) {
	raw := New("test", "connected", nil, "", "", 1)
	base, ok := raw.(model)
	if !ok {
		t.Fatalf("New returned %T, want model", raw)
	}
	m := &base

	m.width = 100
	m.height = 30
	m.scrollSpeed = 15
	m.syncInputSize()
	m.input.Focus()
	lines := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		lines = append(lines, fmt.Sprintf("line %02d", i))
	}
	m.input.SetValue(strings.Join(lines, "\n"))
	m.syncInputSize()
	for i := 0; i < 25; i++ {
		m.input.CursorDown()
	}

	beforeRow := textareaRow(m.input)
	x, y, _, _ := m.inputFieldBounds()
	if !m.mouseInInputField(tea.MouseMsg{X: x + 1, Y: y + 1}) {
		t.Fatal("expected computed coordinates to fall inside the input field")
	}
	msg := tea.MouseMsg{
		X:      x + 1,
		Y:      y + 1,
		Button: tea.MouseButtonWheelUp,
		Action: tea.MouseActionPress,
	}

	m.handleMouse(msg)
	afterRow := textareaRow(m.input)

	if afterRow != beforeRow {
		t.Fatalf("expected textarea cursor row to remain unchanged, before=%d after=%d", beforeRow, afterRow)
	}
	if m.viewport.YOffset != 0 {
		t.Fatalf("expected transcript viewport to remain unchanged, got y offset %d", m.viewport.YOffset)
	}
}

func TestHandleKeySuppressesBracketFragmentAfterMouseWheel(t *testing.T) {
	raw := New("test", "connected", nil, "", "", 1)
	base, ok := raw.(model)
	if !ok {
		t.Fatalf("New returned %T, want model", raw)
	}
	m := &base

	m.width = 100
	m.height = 30
	m.scrollSpeed = 3
	m.syncInputSize()
	m.input.Focus()
	m.input.Reset()

	x, y, _, _ := m.inputFieldBounds()
	m.handleMouse(tea.MouseMsg{
		X:      x + 1,
		Y:      y + 1,
		Button: tea.MouseButtonWheelDown,
		Action: tea.MouseActionPress,
	})

	_, quit := m.handleKey(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune{'['},
	})
	if quit {
		t.Fatal("unexpected quit on bracket fragment")
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected bracket fragment to be suppressed, got input %q", got)
	}
}

func TestHandleMouseScrollsTranscriptWhenPointerOutsideInputField(t *testing.T) {
	raw := New("test", "connected", nil, "", "", 1)
	base, ok := raw.(model)
	if !ok {
		t.Fatalf("New returned %T, want model", raw)
	}
	m := &base

	m.width = 100
	m.height = 30
	m.syncInputSize()
	m.viewport.Height = 10
	m.viewport.SetContent(strings.Repeat("line\n", 50))
	m.viewport.SetYOffset(5)

	x, y, _, _ := m.inputFieldBounds()
	msg := tea.MouseMsg{
		X:      x + 1,
		Y:      y - 1,
		Button: tea.MouseButtonWheelUp,
		Action: tea.MouseActionPress,
	}

	m.handleMouse(msg)

	if got := m.viewport.YOffset; got != 4 {
		t.Fatalf("expected transcript viewport to scroll up, got y offset %d", got)
	}
}

func textareaRow(input any) int {
	value := reflect.ValueOf(input)
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	return int(value.FieldByName("row").Int())
}
