package main

import (
	"context"
	"fmt"

	"femi/brain/agent"
	"femi/brain/femi"
)

// This file is the one place that adapts package agent (claude -p runner)
// to the femi.Thinker interface.

const escalationPrompt = `## This turn is an automatic reminder check-in
Femi started this turn by itself because a reminder was not confirmed; nobody is at the desk to approve anything and your reply is only logged, never spoken. The family set this check-in up in advance, so do NOT call request_approval: use Fo to send exactly the one short, calm, kind text message asked for (say it is from Femi), and confirm with Fo if it asks. Do not send anything else, call, email, book or buy.`

type agentThinker struct{ opts agent.Options }

func (t agentThinker) Think(ctx context.Context, sessionID, text string, onEvent func(femi.Event)) (femi.Result, error) {
	res, err := agent.Run(ctx, t.opts, sessionID, text, func(e agent.Event) {
		onEvent(femi.Event{Kind: e.Kind, Tool: e.Tool, Text: e.Text, Status: e.Status})
	})
	return femi.Result{Reply: res.Reply, SessionID: res.SessionID, CostUSD: res.CostUSD}, err
}

// MCPConfig is the --mcp-config that points Claude at Femi's tool server.
func mcpConfig(toolsPort int) string {
	return fmt.Sprintf(`{"mcpServers":{"femi":{"type":"http","url":"http://127.0.0.1:%d/mcp"}}}`, toolsPort)
}

// newThinkers returns the thinker for normal turns and the one for quiet
// reminder escalations (same tools, extra instructions).
func newThinkers(system string, cfg config) (femi.Thinker, femi.Thinker) {
	var allowed []string
	for _, n := range femi.ToolNames() {
		allowed = append(allowed, "mcp__femi__"+n)
	}
	opts := agent.Options{
		SystemPrompt:      system,
		MCPConfigJSON:     mcpConfig(cfg.toolsPort),
		ExtraAllowedTools: allowed,
		Model:             cfg.model,
		Effort:            cfg.effort,
		Env:               cfg.claudeEnv(),
		WorkDir:           workDir,
		Timeout:           turnBudget,
	}
	esc := opts
	esc.SystemPrompt = system + "\n\n" + escalationPrompt
	return agentThinker{opts}, agentThinker{esc}
}
