package stream

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

type Result struct {
	Bytes        int64
	TTFT         time.Duration
	Started      bool
	Err          error
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
}

const telemetryLimit = 1 << 20

type tokenUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

func applyUsage(result *Result, data []byte) {
	var node struct {
		Usage    tokenUsage `json:"usage"`
		Response *struct {
			Usage tokenUsage `json:"usage"`
		} `json:"response"`
	}
	if json.Unmarshal(data, &node) != nil {
		return
	}
	u := node.Usage
	if node.Response != nil {
		u = node.Response.Usage
	}
	input, output := u.InputTokens, u.OutputTokens
	if input == 0 {
		input = u.PromptTokens
	}
	if output == 0 {
		output = u.CompletionTokens
	}
	if input != 0 || output != 0 || u.TotalTokens != 0 {
		result.InputTokens, result.OutputTokens, result.TotalTokens = input, output, u.TotalTokens
		if result.TotalTokens == 0 {
			result.TotalTokens = input + output
		}
	}
}

func parseTelemetry(result *Result, contentType string, captured []byte) {
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		for _, line := range bytes.Split(captured, []byte{'\n'}) {
			line = bytes.TrimSpace(line)
			if !bytes.HasPrefix(line, []byte("data:")) {
				continue
			}
			data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
			if !bytes.Equal(data, []byte("[DONE]")) {
				applyUsage(result, data)
			}
		}
		return
	}
	applyUsage(result, captured)
}

func Relay(ctx context.Context, w http.ResponseWriter, r io.Reader, started time.Time, contentType ...string) (result Result) {
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32<<10)
	var captured bytes.Buffer
	ct := ""
	if len(contentType) > 0 {
		ct = contentType[0]
	}
	defer func() { parseTelemetry(&result, ct, captured.Bytes()) }()
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if captured.Len() < telemetryLimit {
				remaining := telemetryLimit - captured.Len()
				if n < remaining {
					remaining = n
				}
				_, _ = captured.Write(buf[:remaining])
			}
			if !result.Started {
				result.Started = true
				result.TTFT = time.Since(started)
			}
			written, writeErr := w.Write(buf[:n])
			result.Bytes += int64(written)
			if flusher != nil {
				flusher.Flush()
			}
			if writeErr != nil {
				result.Err = writeErr
				return result
			}
		}
		if err != nil {
			if err != io.EOF {
				result.Err = err
			}
			return result
		}
		select {
		case <-ctx.Done():
			result.Err = ctx.Err()
			return result
		default:
		}
	}
}
