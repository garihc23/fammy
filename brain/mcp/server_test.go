package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testServer(t *testing.T) *httptest.Server {
	t.Helper()
	tools := []Tool{
		{
			Name:        "echo",
			Description: "Echo the text back.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"text": map[string]any{"type": "string"}},
				"required":   []string{"text"},
			},
			Handle: func(ctx context.Context, args json.RawMessage) (string, error) {
				var a struct{ Text string }
				if err := json.Unmarshal(args, &a); err != nil {
					return "", err
				}
				return "echo: " + a.Text, nil
			},
		},
		{
			Name:   "fail",
			Handle: func(context.Context, json.RawMessage) (string, error) { return "", errors.New("nope") },
		},
	}
	ts := httptest.NewServer(NewServer("femi", "0.1", tools))
	t.Cleanup(ts.Close)
	return ts
}

func post(t *testing.T, url, body string) (int, string, http.Header) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

type rpcResp struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

func decode(t *testing.T, body string) rpcResp {
	t.Helper()
	var r rpcResp
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("bad response %q: %v", body, err)
	}
	return r
}

func TestInitialize(t *testing.T) {
	ts := testServer(t)
	code, body, hdr := post(t, ts.URL, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"claude-code","version":"2"}}}`)
	if code != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), "application/json") {
		t.Fatalf("status %d, content-type %q", code, hdr.Get("Content-Type"))
	}
	r := decode(t, body)
	var res struct {
		ProtocolVersion string                     `json:"protocolVersion"`
		Capabilities    map[string]json.RawMessage `json:"capabilities"`
		ServerInfo      struct{ Name string }      `json:"serverInfo"`
	}
	json.Unmarshal(r.Result, &res)
	if string(r.ID) != "1" || res.ProtocolVersion != "2025-03-26" || res.ServerInfo.Name != "femi" || string(res.Capabilities["tools"]) != "{}" {
		t.Fatalf("initialize result %s", body)
	}
}

func TestNotificationAndPing(t *testing.T) {
	ts := testServer(t)
	code, body, _ := post(t, ts.URL, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if code != http.StatusAccepted || body != "" {
		t.Fatalf("notification: %d %q", code, body)
	}
	code, body, _ = post(t, ts.URL, `{"jsonrpc":"2.0","id":"p1","method":"ping"}`)
	r := decode(t, body)
	if code != 200 || string(r.ID) != `"p1"` || string(r.Result) != "{}" {
		t.Fatalf("ping: %d %s", code, body)
	}
}

func TestToolsListAndCall(t *testing.T) {
	ts := testServer(t)
	_, body, _ := post(t, ts.URL, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	var list struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	json.Unmarshal(decode(t, body).Result, &list)
	if len(list.Tools) != 2 || list.Tools[0].Name != "echo" || list.Tools[0].InputSchema["type"] != "object" || list.Tools[1].InputSchema["type"] != "object" {
		t.Fatalf("tools/list: %s", body)
	}

	_, body, _ = post(t, ts.URL, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi"}}}`)
	var res struct {
		Content []struct{ Type, Text string } `json:"content"`
		IsError bool                          `json:"isError"`
	}
	json.Unmarshal(decode(t, body).Result, &res)
	if len(res.Content) != 1 || res.Content[0].Type != "text" || res.Content[0].Text != "echo: hi" || res.IsError {
		t.Fatalf("tools/call: %s", body)
	}

	_, body, _ = post(t, ts.URL, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"fail"}}`)
	res.IsError = false
	json.Unmarshal(decode(t, body).Result, &res)
	if !res.IsError || res.Content[0].Text != "nope" {
		t.Fatalf("failing tool: %s", body)
	}

	_, body, _ = post(t, ts.URL, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"nosuch"}}`)
	if r := decode(t, body); r.Error == nil || r.Error.Code != codeInvalidParams {
		t.Fatalf("unknown tool: %s", body)
	}
}

func TestErrorsAndMethods(t *testing.T) {
	ts := testServer(t)
	_, body, _ := post(t, ts.URL, `{"jsonrpc":"2.0","id":6,"method":"resources/list"}`)
	if r := decode(t, body); r.Error == nil || r.Error.Code != codeMethodNotFound || string(r.ID) != "6" {
		t.Fatalf("unknown method: %s", body)
	}
	code, body, _ := post(t, ts.URL, `{not json`)
	if r := decode(t, body); code != 400 || r.Error == nil || r.Error.Code != codeParseError {
		t.Fatalf("parse error: %d %s", code, body)
	}
	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", resp.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	req.Header.Set("Origin", "http://evil.example")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign origin: %d", resp.StatusCode)
	}
}

func TestBatch(t *testing.T) {
	ts := testServer(t)
	_, body, _ := post(t, ts.URL, `[{"jsonrpc":"2.0","method":"notifications/initialized"},{"jsonrpc":"2.0","id":7,"method":"ping"}]`)
	var rs []rpcResp
	if err := json.Unmarshal([]byte(body), &rs); err != nil || len(rs) != 1 || string(rs[0].ID) != "7" {
		t.Fatalf("batch: %s", body)
	}
}
