package tui

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func clampInt(value int, low int, high int) int {
	if low > high {
		return low
	}
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}
