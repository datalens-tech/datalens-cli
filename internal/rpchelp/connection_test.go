package rpchelp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestConnectionArguments(t *testing.T) {
	c := exampleCatalog()
	for _, tc := range []struct {
		action, args string
		valid        bool
	}{
		{"createConnection", `{"name":"CH","type":"clickhouse","host":"db","port":9000,"workbook_id":"wb"}`, true},
		{"createConnection", `{"name":"PG","type":"postgres","host":"db","port":5432}`, false},
		{"createConnection", `{"name":"SR","type":"starrocks","host":"db","port":9030,"username":"reader"}`, true},
		{"createConnection", `{"name":"Sheet","dir_path":"Folder","url":"https://example.com"}`, false},
		{"createConnection", `{"name":"Sheet","type":"gsheets","dir_path":"Folder","url":"https://example.com"}`, false},
		{"updateConnection", `{"connectionId":"id","data":{"password":"replacement"}}`, true},
		{"updateConnection", `{"connectionId":"id","data":{"url":"https://example.com"}}`, false},
		{"updateConnection", `{"connectionId":"id","data":{"unexpected":true}}`, false},
	} {
		err := c.ValidateArgs(tc.action, "", json.RawMessage(tc.args))
		if (err == nil) != tc.valid {
			t.Errorf("%s %s: want valid=%t, got %v", tc.action, tc.args, tc.valid, err)
		}
	}
	var help strings.Builder
	if err := c.Write(&help, "createConnection", "on-premises", "work"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(help.String(), "Required: name, type\n") {
		t.Errorf("missing required connector fields in help: %s", &help)
	}
}

func TestDateArguments(t *testing.T) {
	c := exampleCatalog()
	var help strings.Builder
	if err := c.Write(&help, "getAuditEntriesUpdates", "on-premises", "work"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(help.String(), ExampleCommands("getAuditEntriesUpdates", `{"from":"2025-01-01T00:00:00Z"}`, " --profile=work")) {
		t.Errorf("missing timestamp example in help: %s", &help)
	}
	if err := c.ValidateArgs("getAuditEntriesUpdates", "", json.RawMessage(`{"from":"2025-01-01T12:34+03:00"}`)); err != nil {
		t.Errorf("rejected a minute-precision timestamp: %v", err)
	}
	for _, invalid := range []string{"your-from", "2025-01-01T1:23:45Z", "2025-01-01T12:34:56,1Z", "2025-01-01T12:34+24:00"} {
		args, _ := json.Marshal(map[string]string{"from": invalid})
		if err := c.ValidateArgs("getAuditEntriesUpdates", "", args); err == nil {
			t.Errorf("accepted invalid timestamp %s", invalid)
		}
	}
	schema, err := compileSchema(json.RawMessage(`{"type":"string","format":"date"}`))
	if err != nil {
		t.Fatal(err)
	}
	var date string
	if err := json.Unmarshal(minimalExample(schema), &date); err != nil {
		t.Fatal(err)
	}
	if _, err := time.Parse(time.DateOnly, date); err != nil || schema.Validate("invalid") == nil {
		t.Errorf("date format validation failed, generated %q", date)
	}
}
