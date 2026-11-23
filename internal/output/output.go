package output

import (
	"bufio"
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"io"
	"unicode/utf8"
)

type Error struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	ExitCode int    `json:"-"`
}

func (e *Error) Error() string {
	return e.Message
}

type Diagnostics struct {
	Version        string `json:"version"`
	Operation      string `json:"operation"`
	NonInteractive bool   `json:"nonInteractive"`
}

type Writer struct {
	Out         io.Writer
	Err         io.Writer
	Format      string
	Diagnostics *Diagnostics
}

func (w Writer) APIResult(result json.RawMessage) error {
	if !utf8.Valid(result) {
		return errors.New("invalid UTF-8 in JSON result")
	}
	switch w.Format {
	case "", "text":
		if err := writeIndented(w.Out, result); err != nil {
			return err
		}
	case "json":
		var data bytes.Buffer
		if err := json.Compact(&data, result); err != nil {
			return err
		}
		data.WriteByte('\n')
		if err := write(w.Out, data.Bytes()); err != nil {
			return err
		}
	default:
		return errors.New("output format must be text or json")
	}
	return w.WriteDiagnostics()
}

func writeIndented(dst io.Writer, data []byte) error {
	if !json.Valid(data) {
		return errors.New("invalid JSON result")
	}
	// Preserve escaped surrogate code units in the original JSON string literals.
	decoder := jsontext.NewDecoder(bytes.NewBuffer(data), jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true))
	out := bufio.NewWriter(checkedWriter{dst})
	indent := []byte{'\n'}
	for {
		kind := decoder.PeekKind()
		container, count := decoder.StackIndex(decoder.StackDepth())
		switch {
		case kind == '}' || kind == ']':
			indent = indent[:len(indent)-2]
			if count > 0 {
				out.Write(indent)
			}
		case container == '{' && count%2 == 1:
			out.WriteString(": ")
		case container == '{' || container == '[':
			if count > 0 {
				out.WriteByte(',')
			}
			out.Write(indent)
		}
		switch kind {
		case '{', '[', '}', ']':
			if _, err := decoder.ReadToken(); err != nil {
				return err
			}
			if err := out.WriteByte(byte(kind)); err != nil {
				return err
			}
			if kind == '{' || kind == '[' {
				indent = append(indent, ' ', ' ')
			}
		default:
			raw, err := decoder.ReadValue()
			if err != nil {
				return err
			}
			if _, err := out.Write(raw); err != nil {
				return err
			}
		}
		if decoder.StackDepth() == 0 {
			out.WriteByte('\n')
			return out.Flush()
		}
	}
}

func (w Writer) Result(value any, text string) error {
	switch w.Format {
	case "", "text":
		if err := write(w.Out, []byte(text)); err != nil {
			return err
		}
		return w.WriteDiagnostics()
	case "json":
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if data[0] != '{' {
			return errors.New("JSON result must be an object")
		}
		if w.Diagnostics != nil {
			var result map[string]json.RawMessage
			if err := json.Unmarshal(data, &result); err != nil {
				return err
			}
			result["diagnostics"], err = json.Marshal(w.Diagnostics)
			if err != nil {
				return err
			}
			data, err = json.Marshal(result)
			if err != nil {
				return err
			}
		}
		return write(w.Out, append(data, '\n'))
	default:
		return errors.New("output format must be text or json")
	}
}

func (w Writer) Failure(failure *Error) error {
	if failure == nil {
		return errors.New("failure must not be nil")
	}
	switch w.Format {
	case "", "text":
		if err := write(w.Err, []byte("Error: "+failure.Message+"\n")); err != nil {
			return err
		}
		return w.WriteDiagnostics()
	case "json":
		result := struct {
			Error       *Error       `json:"error"`
			Diagnostics *Diagnostics `json:"diagnostics,omitempty"`
		}{Error: failure, Diagnostics: w.Diagnostics}
		data, err := json.Marshal(result)
		if err != nil {
			return err
		}
		return write(w.Err, append(data, '\n'))
	default:
		return errors.New("output format must be text or json")
	}
}

func (w Writer) WriteDiagnostics() error {
	if w.Diagnostics == nil {
		return nil
	}
	data, err := json.Marshal(w.Diagnostics)
	if err != nil {
		return err
	}
	return write(w.Err, append(append([]byte("Diagnostics: "), data...), '\n'))
}

func write(destination io.Writer, data []byte) error {
	_, err := (checkedWriter{destination}).Write(data)
	return err
}

type checkedWriter struct{ io.Writer }

func (w checkedWriter) Write(data []byte) (int, error) {
	if w.Writer == nil {
		return 0, errors.New("output stream is not configured")
	}
	n, err := w.Writer.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return n, err
}
