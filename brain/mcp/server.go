// Package mcp is a minimal Model Context Protocol server exposing tools over
// the "streamable HTTP" transport: JSON-RPC 2.0 POSTed to one endpoint,
// answered with plain application/json (no SSE, no sessions).
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// DefaultProtocolVersion is used when the client does not send one.
const DefaultProtocolVersion = "2025-06-18"

// Tool is one callable tool. Handle returns the text shown to the model; a
// non-nil error is returned to the model as a tool error (isError: true).
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any // JSON Schema of the arguments object
	Handle      func(ctx context.Context, args json.RawMessage) (string, error)
}

// Server is an http.Handler serving the tools. Mount it on POST /mcp.
type Server struct {
	Name, Version string
	Instructions  string                           // optional, sent in initialize
	Logf          func(format string, args ...any) // optional

	tools  []Tool
	byName map[string]Tool
}

// NewServer returns a server for tools.
func NewServer(name, version string, tools []Tool) *Server {
	s := &Server{Name: name, Version: version, tools: tools, byName: map[string]Tool{}}
	for _, t := range tools {
		s.byName[t.Name] = t
	}
	return s
}

// JSON-RPC error codes.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func (s *Server) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

func isLocalHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// DNS-rebinding protection: only local callers, never a web page.
	if !isLocalHost(r.Host) {
		http.Error(w, "forbidden host", http.StatusForbidden)
		return
	}
	if o := r.Header.Get("Origin"); o != "" {
		if u, err := url.Parse(o); err != nil || !isLocalHost(u.Host) {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
	}
	if r.Method != http.MethodPost {
		// No server-initiated SSE stream and no sessions to delete.
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	body = bytes.TrimSpace(body)

	var out any
	if len(body) > 0 && body[0] == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(body, &batch); err != nil {
			writeJSON(w, http.StatusBadRequest, errResp(nil, codeParseError, "parse error"))
			return
		}
		var resps []*response
		for _, raw := range batch {
			if resp := s.handleRaw(r.Context(), raw); resp != nil {
				resps = append(resps, resp)
			}
		}
		if len(resps) > 0 {
			out = resps
		}
	} else if resp := s.handleRaw(r.Context(), body); resp != nil {
		if resp.Error != nil && resp.Error.Code == codeParseError {
			writeJSON(w, http.StatusBadRequest, resp)
			return
		}
		out = resp
	}
	if out == nil {
		// Only notifications or client responses: accepted, no body.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func errResp(id json.RawMessage, code int, msg string) *response {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return &response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
}

// handleRaw handles one JSON-RPC message; nil means "no response" (a
// notification, or a response sent by the client).
func (s *Server) handleRaw(ctx context.Context, raw json.RawMessage) *response {
	var m message
	if err := json.Unmarshal(raw, &m); err != nil {
		return errResp(nil, codeParseError, "parse error")
	}
	if m.Method == "" {
		if len(m.ID) > 0 {
			return nil // a response to something we never asked; ignore
		}
		return errResp(nil, codeInvalidRequest, "invalid request")
	}
	if len(m.ID) == 0 {
		s.logf("mcp: notification %s", m.Method)
		return nil
	}
	result, rerr := s.dispatch(ctx, m)
	if rerr != nil {
		return &response{JSONRPC: "2.0", ID: m.ID, Error: rerr}
	}
	return &response{JSONRPC: "2.0", ID: m.ID, Result: result}
}

func (s *Server) dispatch(ctx context.Context, m message) (any, *rpcError) {
	switch m.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
			ClientInfo      struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"clientInfo"`
		}
		_ = json.Unmarshal(m.Params, &p)
		v := p.ProtocolVersion
		if v == "" {
			v = DefaultProtocolVersion
		}
		s.logf("mcp: initialize from %s %s (protocol %s)", p.ClientInfo.Name, p.ClientInfo.Version, v)
		res := map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": s.Name, "version": s.Version},
		}
		if s.Instructions != "" {
			res["instructions"] = s.Instructions
		}
		return res, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		list := make([]map[string]any, 0, len(s.tools))
		for _, t := range s.tools {
			schema := t.InputSchema
			if schema == nil {
				schema = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			list = append(list, map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"inputSchema": schema,
			})
		}
		return map[string]any{"tools": list}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil || p.Name == "" {
			return nil, &rpcError{Code: codeInvalidParams, Message: "tools/call needs a tool name"}
		}
		t, ok := s.byName[p.Name]
		if !ok {
			return nil, &rpcError{Code: codeInvalidParams, Message: "unknown tool: " + p.Name}
		}
		args := p.Arguments
		if len(args) == 0 || string(args) == "null" {
			args = json.RawMessage("{}")
		}
		text, err := s.call(ctx, t, args)
		isErr := err != nil
		if isErr {
			text = err.Error()
		}
		s.logf("mcp: %s %s -> %q (error=%v)", t.Name, args, text, isErr)
		return map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
			"isError": isErr,
		}, nil
	}
	return nil, &rpcError{Code: codeMethodNotFound, Message: "method not found: " + m.Method}
}

func (s *Server) call(ctx context.Context, t Tool, args json.RawMessage) (text string, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("internal error in %s: %v", t.Name, p)
		}
	}()
	if t.Handle == nil {
		return "", fmt.Errorf("%s is not implemented", t.Name)
	}
	return t.Handle(ctx, args)
}
