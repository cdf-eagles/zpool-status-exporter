package collector

import (
	"iter"
	"strings"
)

// lines yields each line of command output without its line ending. The
// lines share one copy of the output.
func lines(data []byte) iter.Seq[string] {
	return func(yield func(string) bool) {
		for line := range strings.Lines(string(data)) {
			line = strings.TrimSuffix(line, "\n")
			if !yield(strings.TrimSuffix(line, "\r")) {
				return
			}
		}
	}
}
