package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestIndentedAPIResult(t *testing.T) {
	for _, input := range []string{
		"  null\r\n", `"top-level string"`, `1.2300e+2000`, `{"a":{},"b":[ [], [{}] ]}`,
		`{ "\u0061":90071992547409931234567890, "a":["\/","\\\"","\ud800",-0.0E-9999], "a":true }`,
		`"😃 и ёж"`,
	} {
		var stdout, stderr, want bytes.Buffer
		w := Writer{Out: &stdout, Err: &stderr, Format: "text", Diagnostics: &Diagnostics{Operation: "rpc"}}
		if err := w.APIResult([]byte(input)); err != nil {
			t.Fatal(err)
		}
		if err := json.Indent(&want, bytes.TrimSpace([]byte(input)), "", "  "); err != nil {
			t.Fatal(err)
		}
		want.WriteByte('\n')
		if stdout.String() != want.String() || !strings.Contains(stderr.String(), `"operation":"rpc"`) {
			t.Errorf("input %q: stdout=%q, stderr=%q, want stdout=%q", input, &stdout, &stderr, &want)
		}
	}
}

func TestIndentedAPIResultWritesIncrementally(t *testing.T) {
	input := "[" + strings.Repeat("1,", 2999) + "2]"
	out := new(countingWriter)
	if err := (Writer{Out: out}).APIResult([]byte(input)); err != nil {
		t.Fatal(err)
	}
	if out.writes < 2 {
		t.Fatalf("wrote the whole document in %d call(s)", out.writes)
	}
}

type countingWriter struct{ writes int }

func (w *countingWriter) Write(p []byte) (int, error) {
	w.writes++
	return len(p), nil
}

func TestAPIResultRejectsInvalidDocumentBeforeWriting(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		for _, input := range []string{`{"done":true} 0`, "[\"" + strings.Repeat("x", 5000) + "\",\"\xff\"]"} {
			var stdout, stderr bytes.Buffer
			w := Writer{Out: &stdout, Err: &stderr, Format: format, Diagnostics: &Diagnostics{}}
			if err := w.APIResult([]byte(input)); err == nil || stdout.Len() != 0 || stderr.Len() != 0 {
				t.Errorf("%s: err=%v stdout bytes=%d stderr=%q", format, err, stdout.Len(), &stderr)
			}
		}
	}
}

func TestJSONResult(t *testing.T) {
	for _, debug := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "debug"}[debug], func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			writer := Writer{Out: &stdout, Err: &stderr, Format: "json"}
			if debug {
				writer.Diagnostics = &Diagnostics{Version: "0.1.0", Operation: "version", NonInteractive: true}
			}
			value := struct {
				Name string `json:"name"`
			}{Name: "example\nworkbook"}
			if err := writer.Result(value, "not JSON\n"); err != nil {
				t.Fatal(err)
			}
			result := decodeDocument(t, stdout.String())
			want := map[string]any{"name": "example\nworkbook"}
			if debug {
				want["diagnostics"] = map[string]any{
					"version": "0.1.0", "operation": "version", "nonInteractive": true,
				}
			}
			if !reflect.DeepEqual(result, want) {
				t.Fatalf("result = %#v, want %#v", result, want)
			}
			if stderr.Len() != 0 {
				t.Fatalf("unexpected stderr: %s", &stderr)
			}
		})
	}
}

func TestJSONFailure(t *testing.T) {
	for _, debug := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "debug"}[debug], func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			writer := Writer{Out: &stdout, Err: &stderr, Format: "json"}
			if debug {
				writer.Diagnostics = &Diagnostics{Version: "dev", Operation: "config profile use"}
			}
			failure := &Error{Code: "profile_not_found", Message: "Profile does not exist", ExitCode: 2}
			if failure.Error() != failure.Message {
				t.Fatalf("Error() = %q", failure.Error())
			}
			if err := writer.Failure(failure); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"error": map[string]any{
				"code": "profile_not_found", "message": "Profile does not exist",
			}}
			if debug {
				want["diagnostics"] = map[string]any{
					"version": "dev", "operation": "config profile use", "nonInteractive": false,
				}
			}
			if result := decodeDocument(t, stderr.String()); !reflect.DeepEqual(result, want) {
				t.Fatalf("failure = %#v, want %#v", result, want)
			}
			if stdout.Len() != 0 {
				t.Fatalf("unexpected stdout: %s", &stdout)
			}
		})
	}
}

func TestJSONResultDoesNotWriteInvalidData(t *testing.T) {
	values := []any{
		map[string]any{"name": "valid", "invalid": make(chan int)},
		[]string{"array"},
		"string",
		42,
		nil,
		map[string]any(nil),
	}
	for _, value := range values {
		var stdout, stderr bytes.Buffer
		writer := Writer{Out: &stdout, Err: &stderr, Format: "json", Diagnostics: &Diagnostics{Version: "dev"}}
		if err := writer.Result(value, "ignored"); err == nil {
			t.Errorf("Result(%#v) succeeded", value)
		}
		if stdout.Len() != 0 || stderr.Len() != 0 {
			t.Errorf("Result(%#v) wrote stdout=%q stderr=%q", value, stdout.String(), stderr.String())
		}
	}
}

func TestTextStreams(t *testing.T) {
	for _, format := range []string{"", "text"} {
		var stdout, stderr bytes.Buffer
		writer := Writer{Out: &stdout, Err: &stderr, Format: format}
		if err := writer.Result(map[string]any{"name": "example"}, "example\n"); err != nil {
			t.Fatal(err)
		}
		if stdout.String() != "example\n" || stderr.Len() != 0 {
			t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
		writer.Diagnostics = &Diagnostics{Version: "dev", Operation: "version"}
		if err := writer.Failure(&Error{Code: "failed", Message: "A safe failure", ExitCode: 1}); err != nil {
			t.Fatal(err)
		}
		want := "Error: A safe failure\nDiagnostics: {\"version\":\"dev\",\"operation\":\"version\",\"nonInteractive\":false}\n"
		if stderr.String() != want || stdout.String() != "example\n" {
			t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
		stderr.Reset()
		stdout.Reset()
		if err := writer.Result(map[string]any{}, "exact text"); err != nil {
			t.Fatal(err)
		}
		if stdout.String() != "exact text" || stderr.String() != strings.TrimPrefix(want, "Error: A safe failure\n") {
			t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
	}
}

func TestWriteFailures(t *testing.T) {
	broken := errors.New("broken pipe")
	for _, format := range []string{"text", "json"} {
		for _, failure := range []struct {
			writer io.Writer
			err    error
		}{
			{writer: failingWriter{err: broken}, err: broken},
			{writer: failingWriter{}, err: io.ErrShortWrite},
		} {
			writer := Writer{Out: failure.writer, Err: failure.writer, Format: format}
			if err := writer.APIResult(json.RawMessage(`{"ok":true}`)); !errors.Is(err, failure.err) {
				t.Errorf("%s API result error = %v, want %v", format, err, failure.err)
			}
			if err := writer.Result(map[string]any{}, "result"); !errors.Is(err, failure.err) {
				t.Errorf("%s result error = %v, want %v", format, err, failure.err)
			}
			if err := writer.Failure(&Error{Message: "failure"}); !errors.Is(err, failure.err) {
				t.Errorf("%s failure error = %v, want %v", format, err, failure.err)
			}
		}
	}
	writer := Writer{Out: io.Discard, Err: failingWriter{err: broken}, Format: "text", Diagnostics: &Diagnostics{}}
	if err := writer.Result(map[string]any{}, "result"); !errors.Is(err, broken) {
		t.Errorf("diagnostic error = %v, want %v", err, broken)
	}
}

func TestInvalidWriterUse(t *testing.T) {
	var stdout, stderr bytes.Buffer
	writer := Writer{Out: &stdout, Err: &stderr, Format: "xml"}
	if err := writer.Result(map[string]any{}, "ignored"); err == nil {
		t.Fatal("unsupported result format succeeded")
	}
	if err := writer.Failure(&Error{Message: "ignored"}); err == nil {
		t.Fatal("unsupported failure format succeeded")
	}
	writer.Format = "json"
	if err := writer.Failure(nil); err == nil {
		t.Fatal("nil failure succeeded")
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func decodeDocument(t *testing.T, data string) map[string]any {
	t.Helper()
	if !strings.HasSuffix(data, "\n") {
		t.Fatalf("missing newline: %q", data)
	}
	decoder := json.NewDecoder(strings.NewReader(data))
	var result map[string]any
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("extra output after JSON document: %v, %v", extra, err)
	}
	return result
}

type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) {
	return 0, w.err
}
