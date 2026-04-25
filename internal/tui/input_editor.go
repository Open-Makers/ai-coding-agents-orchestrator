package tui

import (
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

// runeLen returns the number of runes in s.
func runeLen(s string) int { return utf8.RuneCountInString(s) }

// clampRunePos clamps pos into [0, runeLen(s)].
func clampRunePos(s string, pos int) int {
	n := runeLen(s)
	if pos < 0 {
		return 0
	}
	if pos > n {
		return n
	}
	return pos
}

// runeByteOffset returns the byte offset of the runePos-th rune in s.
// runePos must be in [0, runeLen(s)].
func runeByteOffset(s string, runePos int) int {
	i, count := 0, 0
	for i < len(s) && count < runePos {
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
		count++
	}
	return i
}

// runeInsert inserts runes at runePos and returns the new string and cursor position.
func runeInsert(s string, runePos int, runes []rune) (string, int) {
	runePos = clampRunePos(s, runePos)
	off := runeByteOffset(s, runePos)
	ins := string(runes)
	return s[:off] + ins + s[off:], runePos + len(runes)
}

// runeDeleteBefore removes the rune immediately before runePos.
func runeDeleteBefore(s string, runePos int) (string, int) {
	runePos = clampRunePos(s, runePos)
	if runePos == 0 {
		return s, 0
	}
	end := runeByteOffset(s, runePos)
	start := runeByteOffset(s, runePos-1)
	return s[:start] + s[end:], runePos - 1
}

// runeDeleteAt removes the rune at runePos (Delete key).
func runeDeleteAt(s string, runePos int) (string, int) {
	runePos = clampRunePos(s, runePos)
	if runePos >= runeLen(s) {
		return s, runePos
	}
	start := runeByteOffset(s, runePos)
	end := runeByteOffset(s, runePos+1)
	return s[:start] + s[end:], runePos
}

// renderInputWithCursor returns the input rendered with a block cursor at runePos.
func renderInputWithCursor(input string, runePos int) string {
	cursor := lipgloss.NewStyle().Reverse(true)
	runePos = clampRunePos(input, runePos)
	if runePos >= runeLen(input) {
		return input + cursor.Render(" ")
	}
	off := runeByteOffset(input, runePos)
	_, size := utf8.DecodeRuneInString(input[off:])
	return input[:off] + cursor.Render(input[off:off+size]) + input[off+size:]
}
