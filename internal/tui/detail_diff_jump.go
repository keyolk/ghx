package tui

// Coarse navigation in the diff view. j/k step one row, which is the right
// granularity for reading a hunk and the wrong one for finding the next change
// in a 2000-line diff — reaching the fourth file that way is a page-down
// marathon. J/K jump hunk to hunk, { and } jump file to file.
//
// Both work off the same flattened row list the cursor already uses, so a jump
// lands on a row `c` can comment from and the paired layout stays in step.
// setCursor is the entry point rather than assigning v.cursor: it invalidates
// both scroll offsets so the destination is centred instead of scrolled to a
// screen edge, and re-derives the focused column.

// jumpHunk moves the cursor to the header of the next (delta > 0) or previous
// (delta < 0) hunk, counting a file header as a boundary too: the first hunk of
// the next file is the next change, and stopping short of it would leave J
// stuck at the end of every file.
//
// Reports whether the cursor moved, so the caller can leave the key alone when
// there is nowhere to go — pressing J at the last hunk should do nothing rather
// than snap back to the top.
func (v *diffView) jumpHunk(delta int) bool {
	return v.jumpBoundary(delta, func(r diffRow) bool {
		return r.kind == rowHunkHeader || r.kind == rowFileHeader
	})
}

// jumpFile moves the cursor to the next or previous file header. A folded file
// contributes only its header, so folding a file you are done with turns { and }
// into a table of contents.
func (v *diffView) jumpFile(delta int) bool {
	return v.jumpBoundary(delta, func(r diffRow) bool {
		return r.kind == rowFileHeader
	})
}

// jumpBoundary scans for the nearest row matching want in the given direction.
func (v *diffView) jumpBoundary(delta int, want func(diffRow) bool) bool {
	if len(v.rows) == 0 || delta == 0 {
		return false
	}
	step := 1
	if delta < 0 {
		step = -1
	}
	for i := v.cursor + step; i >= 0 && i < len(v.rows); i += step {
		if !want(v.rows[i]) {
			continue
		}
		v.setCursor(i)
		return true
	}
	// Already at the last (or first) boundary. Staying put says so; wrapping
	// around would silently send the reviewer back to a file they finished.
	return false
}
