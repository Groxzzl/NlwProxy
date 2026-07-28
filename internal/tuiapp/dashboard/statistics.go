package dashboard

import (
	"math"
	"sort"
	"strings"
	"time"
)

// Percentile uses nearest-rank semantics and never mutates its input.
func Percentile(values []time.Duration, quantile float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]time.Duration(nil), values...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	if quantile <= 0 {
		return ordered[0]
	}
	if quantile >= 1 {
		return ordered[len(ordered)-1]
	}
	index := int(math.Ceil(quantile*float64(len(ordered)))) - 1
	return ordered[index]
}

func Sparkline(values []float64) string {
	if len(values) == 0 {
		return ""
	}
	min, max := values[0], values[0]
	for _, value := range values[1:] {
		if value < min {
			min = value
		}
		if value > max {
			max = value
		}
	}
	const blocks = "▁▂▃▄▅▆▇█"
	var out strings.Builder
	if max == min {
		for range values {
			out.WriteRune('▁')
		}
		return out.String()
	}
	for _, value := range values {
		index := int(math.Round((value - min) / (max - min) * 7))
		out.WriteString(string([]rune(blocks)[index]))
	}
	return out.String()
}
