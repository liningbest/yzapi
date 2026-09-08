// Package convert translates requests and responses between wire protocols.
package convert

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
)

// ErrIncomplete is returned when an upstream stream ends without its protocol's
// terminal event ([DONE], message_stop, response.completed ...).
var ErrIncomplete = errors.New("upstream stream ended without a terminal event")

// Event is a parsed server-sent event.
type Event struct {
	Event string
	Data  string
}

// SSEReader yields events from an SSE stream.
type SSEReader struct {
	r *bufio.Reader
}

func NewSSEReader(r io.Reader) *SSEReader {
	return &SSEReader{r: bufio.NewReaderSize(r, 64*1024)}
}

// Next returns the next event or io.EOF.
func (s *SSEReader) Next() (Event, error) {
	var ev Event
	var data []string
	for {
		line, err := s.r.ReadString('\n')
		if err != nil {
			if err == io.EOF && (len(data) > 0 || ev.Event != "") {
				ev.Data = strings.Join(data, "\n")
				return ev, nil
			}
			return ev, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if len(data) == 0 && ev.Event == "" {
				continue
			}
			ev.Data = strings.Join(data, "\n")
			return ev, nil
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			ev.Event = strings.TrimSpace(line[6:])
		} else if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
}

// WriteSSE formats one event.
func WriteSSE(w io.Writer, event, data string) error {
	var b bytes.Buffer
	if event != "" {
		b.WriteString("event: ")
		b.WriteString(event)
		b.WriteByte('\n')
	}
	b.WriteString("data: ")
	b.WriteString(data)
	b.WriteString("\n\n")
	_, err := w.Write(b.Bytes())
	return err
}
