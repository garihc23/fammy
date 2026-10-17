// Command ask runs one Femi turn through Claude Code and prints the streamed
// events and the final reply. Manual check for package agent:
//
//	go run ./cmd/ask "what's the weather in Mumbai?"
//	go run ./cmd/ask -resume <session-id> "and tomorrow?"
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"femi/brain/agent"
)

const defaultSystemPrompt = `You are Femi, a friendly voice assistant for a family. Your replies are spoken aloud by a small device, so answer in one or two short, plain sentences: no markdown, lists, links or emoji. For real-world information and errands, use the Fo tools: send the request with ask_fo, and if it returns status "working", keep calling get_fo_reply until it is complete or failed. Never show internal ids.`

func main() {
	model := flag.String("model", "", "model alias or name (default: Claude Code's default)")
	effort := flag.String("effort", "", "low, medium or high (default: Claude Code's default)")
	resume := flag.String("resume", "", "session id to continue")
	workDir := flag.String("workdir", "", "cwd for claude (default <tmp>/femi-agent)")
	timeout := flag.Duration("timeout", agent.DefaultTimeout, "whole-turn timeout")
	system := flag.String("system", defaultSystemPrompt, "text appended to the system prompt")
	debug := flag.Bool("debug", false, "log skipped stream lines")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: ask [flags] <what to ask>\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	text := strings.TrimSpace(strings.Join(flag.Args(), " "))
	if text == "" {
		flag.Usage()
		os.Exit(2)
	}
	if *debug {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	start := time.Now()
	opts := agent.Options{SystemPrompt: *system, Model: *model, Effort: *effort, WorkDir: *workDir, Timeout: *timeout}
	res, err := agent.Run(ctx, opts, *resume, text, func(e agent.Event) {
		tool := e.Tool
		if e.Status != "" {
			tool += "/" + e.Status
		}
		fmt.Printf("[%6.1fs] %-11s %-22s %s\n", time.Since(start).Seconds(), e.Kind, tool, oneLine(e.Text, 160))
	})
	wall := time.Since(start)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		if res.SessionID != "" {
			fmt.Fprintf(os.Stderr, "session: %s\n", res.SessionID)
		}
		os.Exit(1)
	}
	fmt.Printf("\nreply:   %s\nsession: %s\nturn:    %.1fs (wall %.1fs)  cost: $%.4f\n",
		res.Reply, res.SessionID, res.Duration.Seconds(), wall.Seconds(), res.CostUSD)
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
