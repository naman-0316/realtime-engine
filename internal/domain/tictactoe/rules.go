package tictactoe

// winLines enumerates all 8 winning triples as board indices (0-8, row-major:
// index = y*3 + x).
var winLines = [8][3]int{
	{0, 1, 2}, {3, 4, 5}, {6, 7, 8}, // rows
	{0, 3, 6}, {1, 4, 7}, {2, 5, 8}, // columns
	{0, 4, 8}, {2, 4, 6}, // diagonals
}

// winningSeat returns the seat index (0 or 1) of the player occupying a
// complete winning line on board, or -1 if there is no winner yet.
func winningSeat(board [9]seat) int {
	for _, line := range winLines {
		a, b, c := board[line[0]], board[line[1]], board[line[2]]
		if a != emptySeat && a == b && b == c {
			return int(a) - 1
		}
	}
	return -1
}
