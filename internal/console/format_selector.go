package console

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// SelectFormat is the portable fallback for the C format selector. Numbered
// entries keep redirected consoles usable; Enter chooses the first format.
func SelectFormat(in io.Reader, out io.Writer, labels []string) (int, bool, error) {
	if len(labels) == 0 {
		return 0, false, nil
	}
	fmt.Fprintln(out, "Copy agent configuration (choose format):")
	for i, label := range labels {
		fmt.Fprintf(out, "  %d) %s\n", i+1, label)
	}
	fmt.Fprint(out, "> ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && err != io.EOF {
		return 0, false, err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return 0, true, nil
	}
	if strings.EqualFold(line, "q") || strings.EqualFold(line, "esc") {
		return 0, false, nil
	}
	n, err := strconv.Atoi(line)
	if err != nil || n < 1 || n > len(labels) {
		return 0, false, fmt.Errorf("invalid format selection")
	}
	return n - 1, true, nil
}
