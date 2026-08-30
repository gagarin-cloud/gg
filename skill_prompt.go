package main

import (
	"bufio"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

// pickAgentsInteractively shows a checkbox list of known agent harnesses and
// returns the keys the user selected. It needs a real terminal on both ends —
// piped input has no arrow keys to read — so callers should fall back to
// --agent when this can't run.
func pickAgentsInteractively(out io.Writer) ([]string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return nil, fmt.Errorf("--interactive needs a terminal; pass --agent instead")
	}

	prevState, err := term.MakeRaw(fd)
	if err != nil {
		return nil, fmt.Errorf("putting the terminal in raw mode: %w", err)
	}
	defer term.Restore(fd, prevState)

	selected := make([]bool, len(agentTargets))
	cursor := 0
	r := bufio.NewReader(os.Stdin)

	redraw := func() {
		// \r\n throughout: raw mode disables the newline translation a normal
		// terminal does, so a bare \n would stair-step the output.
		fmt.Fprintf(out, "\r\nInstall the gagarin skill for which agents?\r\n")
		fmt.Fprintf(out, "(space to toggle, a to select all, enter to confirm, q to cancel)\r\n\r\n")
		for i, a := range agentTargets {
			box, arrow := "[ ]", "  "
			if selected[i] {
				box = "[x]"
			}
			if i == cursor {
				arrow = "> "
			}
			fmt.Fprintf(out, "%s%s %s\r\n", arrow, box, a.displayName)
		}
	}

	moveCursorUp := func(n int) { fmt.Fprintf(out, "\x1b[%dA", n) }
	clearDown := func() { fmt.Fprint(out, "\x1b[J") }

	fmt.Fprint(out, "\r\n")
	linesDrawn := 0
	for {
		if linesDrawn > 0 {
			moveCursorUp(linesDrawn)
			clearDown()
		}
		redraw()
		// redraw() ends exactly this many lines below where it started: two
		// blank/heading lines, the instructions line plus its trailing blank,
		// and one line per agent.
		linesDrawn = len(agentTargets) + 4

		b, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		switch b {
		case 'q', 3: // q, or ctrl-c
			return nil, fmt.Errorf("cancelled")
		case 27: // Escape, or the start of an arrow-key sequence (ESC '[' 'A'/'B')
			b2, err := r.ReadByte()
			if err != nil {
				return nil, fmt.Errorf("cancelled")
			}
			if b2 != '[' {
				return nil, fmt.Errorf("cancelled")
			}
			b3, err := r.ReadByte()
			if err != nil {
				continue
			}
			switch b3 {
			case 'A':
				cursor = (cursor - 1 + len(agentTargets)) % len(agentTargets)
			case 'B':
				cursor = (cursor + 1) % len(agentTargets)
			}
			continue
		case ' ':
			selected[cursor] = !selected[cursor]
			continue
		case 'a':
			all := true
			for _, s := range selected {
				all = all && s
			}
			for i := range selected {
				selected[i] = !all
			}
			continue
		case '\r', '\n':
			var keys []string
			for i, s := range selected {
				if s {
					keys = append(keys, agentTargets[i].key)
				}
			}
			if len(keys) == 0 {
				continue
			}
			fmt.Fprint(out, "\r\n")
			return keys, nil
		case 'j':
			cursor = (cursor + 1) % len(agentTargets)
			continue
		case 'k':
			cursor = (cursor - 1 + len(agentTargets)) % len(agentTargets)
			continue
		}
	}
}
