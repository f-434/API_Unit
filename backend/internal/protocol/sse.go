package protocol

import (
	"bufio"
	"io"
	"strings"
	"unicode/utf8"
)

// SSEEvent is one dispatched server-sent event.
type SSEEvent struct {
	Name string
	Data string
}

// readSSE implements the parts of the SSE spec that matter for LLM streams:
// multi-line data fields are joined with '\n', comments are ignored and the
// final pending event is dispatched at EOF.
func readSSE(r io.Reader, emit func(SSEEvent) error) error {
	sc := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 16*1024*1024)

	var event string
	var data []string
	dispatch := func() error {
		if len(data) == 0 && event == "" {
			return nil
		}
		ev := SSEEvent{Name: event, Data: strings.Join(data, "\n")}
		event = ""
		data = data[:0]
		return emit(ev)
	}

	for sc.Scan() {
		line := strings.TrimSuffix(sc.Text(), "\r")
		if line == "" {
			if err := dispatch(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if !found {
			field, value = line, ""
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			event = value
		case "data":
			data = append(data, value)
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return dispatch()
}

// capture accumulates raw upstream payloads with a hard byte limit so a
// runaway stream cannot exhaust memory.
type capture struct {
	b     strings.Builder
	limit int
	trunc bool
}

func newCapture(limit int) *capture { return &capture{limit: limit} }

func (c *capture) Add(s string) {
	if c == nil || c.trunc {
		return
	}
	if c.b.Len()+len(s) > c.limit {
		allow := c.limit - c.b.Len()
		for allow > 0 && !utf8.RuneStart(s[allow]) {
			allow--
		}
		if allow > 0 {
			c.b.WriteString(s[:allow])
		}
		c.trunc = true
		return
	}
	c.b.WriteString(s)
}

func (c *capture) String() string {
	if c == nil {
		return ""
	}
	return c.b.String()
}
