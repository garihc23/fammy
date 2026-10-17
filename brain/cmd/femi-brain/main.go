// Command femi-brain is Femi's brain: it serves the device link (TCP :8787),
// Femi's own MCP tools (http://127.0.0.1:8788/mcp) and the reminder
// scheduler, and runs each request through Cartesia STT → Claude Code (with
// Fo + Femi tools) → Cartesia TTS.
//
//	cd /home/leog/femi/brain && go run ./cmd/femi-brain
//
// Config comes from the environment, falling back to /home/leog/femi/brain/.env
// (KEY=VALUE lines):
//
//	CARTESIA_API_KEY   voice in/out (without it: text events only, replies as captions)
//	FEMI_PORT          device link port (8787)
//	FEMI_TOOLS_PORT    MCP tools port on 127.0.0.1 (8788)
//	FAMILY_DIR         profile.json + photos/ (/home/leog/femi/family)
//	FEMI_ACK_MINUTES   minutes before an unacknowledged reminder escalates (10)
//	FEMI_MODEL         Claude model for turns (claude-haiku-5-5: the quickest to answer)
//	FEMI_EFFORT        how hard Claude thinks before answering: low, medium, high (low)
//	FEMI_CAMERA        webcam for look_at_camera (/dev/video0); "none" turns it off
//	FEMI_AUTH          login (Claude Code's login; Fo through claude.ai) or key (ANTHROPIC_API_KEY, ~2 s faster
//	                   per turn; claude.ai connectors are off, so Fo must be signed in directly: /mcp > fo)
//	ANTHROPIC_API_KEY  with FEMI_AUTH=key
//	ANTHROPIC_WORKSPACE_ID  for a key that isn't scoped to one workspace
//	FEMI_EMOTION       how Famyy's voice sounds: content, calm, ... (content); "none" lets Cartesia choose
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"femi/brain/camera"
	"femi/brain/cartesia"
	"femi/brain/femi"
	"femi/brain/link"
	"femi/brain/mcp"
	"femi/brain/remind"
)

const (
	brainDir   = "/home/leog/femi/brain"
	envFile    = brainDir + "/.env"
	workDir    = brainDir + "/workdir" // stable: --resume finds sessions per cwd; its settings disable user hooks
	turnBudget = 4 * time.Minute       // one Claude turn (Fo research can take a couple of minutes)
)

type config struct {
	cartesiaKey string
	port        int
	toolsPort   int
	familyDir   string
	ackWindow   time.Duration
	model       string
	effort      string
	camera      string
	auth        string
	apiKey      string
	workspace   string
	emotion     string
	promptPath  string
}

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}

	profile, err := femi.LoadProfile(filepath.Join(cfg.familyDir, "profile.json"))
	if err != nil {
		log.Fatalf("profile: %v", err)
	}
	if profile == nil {
		log.Printf("no %s/profile.json (see profile.example.json): running without family context", cfg.familyDir)
	}
	prompt, err := os.ReadFile(cfg.promptPath)
	if err != nil {
		log.Fatalf("system prompt: %v", err)
	}
	system := strings.TrimSpace(string(prompt))
	if ctx := profile.PromptContext(); ctx != "" {
		system += "\n\n## Family profile\n" + ctx
	}

	if _, err := exec.LookPath("claude"); err != nil {
		log.Printf("WARNING: claude not found on PATH; every turn will fail")
	}
	thinker, escalator, watcher := newThinkers(system, cfg)

	var ears femi.Ears
	var voice femi.Voice
	if cfg.cartesiaKey != "" {
		c := cartesia.New(cfg.cartesiaKey)
		c.Emotion = cfg.emotion
		if c.Emotion == "none" {
			c.Emotion = ""
		}
		ears, voice = c, c
		log.Printf("voice: Cartesia STT + TTS (emotion %q)", c.Emotion)
	} else {
		log.Printf("voice: CARTESIA_API_KEY not set: text events only, replies sent as captions (SAY with 0 bytes)")
	}

	sched := remind.New(cfg.ackWindow)
	sched.Path = filepath.Join(cfg.familyDir, "reminders.json") // reminders and medicines survive restarts
	if err := sched.Load(); err != nil {
		log.Printf("reminders: %v (starting with none)", err)
	} else {
		log.Printf("reminders: %d loaded from %s", len(sched.List()), sched.Path)
	}
	dev := &link.Server{Logf: log.Printf}
	brain := femi.New(femi.Config{
		Device:      dev,
		Thinker:     thinker,
		Escalator:   escalator,
		FoWatcher:   watcher,
		Ears:        ears,
		Voice:       voice,
		Reminders:   sched,
		Profile:     profile,
		FamilyDir:   cfg.familyDir,
		RecordDir:   filepath.Join(filepath.Dir(brainDir), "qa", "recordings"),
		Camera:      cfg.webcam(),
		FoVoice:     cartesia.VoiceJacqueline,
		CallVoice:   cartesia.VoiceDev,
		TurnTimeout: turnBudget + 30*time.Second,
	})
	dev.Handler = brain
	if profile != nil {
		for _, m := range profile.Medications {
			r, err := sched.AddDaily(m.Time, m.Who, m.What, m.EscalateTo, time.Now())
			if err != nil {
				log.Printf("profile: medication %q for %s: %v", m.What, m.Who, err)
				continue
			}
			log.Printf("medication %s", remind.Describe(r, time.Now()))
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP) // SIGHUP: its terminal tab closed
	defer stop()

	// Femi's MCP tools, local only.
	toolsAddr := fmt.Sprintf("127.0.0.1:%d", cfg.toolsPort)
	toolsLn, err := net.Listen("tcp", toolsAddr)
	if err != nil {
		log.Fatalf("tools server: %v", err)
	}
	tools := mcp.NewServer("femi", "0.1", brain.Tools())
	tools.Logf = log.Printf
	mux := http.NewServeMux()
	mux.Handle("/mcp", tools)
	// GET /status: {"busy":false,...}; restarts wait until nothing is in flight
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(brain.Status())
	})
	httpSrv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := httpSrv.Serve(toolsLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("tools server: %v", err)
		}
	}()
	log.Printf("tools: MCP on http://%s/mcp (%d tools)", toolsAddr, len(femi.ToolNames()))
	thinker.start() // Claude connects to the tools as it starts, so only now
	log.Printf("claude: a session is warming up (%s, %s effort, on the %s)", cfg.model, cfg.effort, cfg.auth)

	linkLn, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.port))
	if err != nil {
		log.Fatalf("device link: %v", err)
	}
	go func() {
		if err := dev.Serve(ctx, linkLn); err != nil {
			log.Fatalf("device link: %v", err)
		}
	}()
	go sched.Run(ctx)

	ips := lanIPs()
	log.Printf("device link: listening on :%d", cfg.port)
	if len(ips) == 0 {
		log.Printf("no LAN IPv4 address found: is Wi-Fi up?")
	}
	for _, ip := range ips {
		log.Printf(`LAN address %-15s (%s): for the device's secrets.h: #define BRAIN_HOST "%s"`, ip[0], ip[1], ip[0])
	}

	<-ctx.Done()
	log.Printf("shutting down")
	thinker.Close()
	shut, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shut)
}

func loadConfig() (config, error) {
	file := readEnvFile(envFile)
	get := func(key, def string) string {
		if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
		if v := file[key]; v != "" {
			return v
		}
		return def
	}
	cfg := config{
		cartesiaKey: get("CARTESIA_API_KEY", ""),
		familyDir:   get("FAMILY_DIR", "/home/leog/femi/family"),
		model:       get("FEMI_MODEL", "claude-haiku-5-5"),
		effort:      get("FEMI_EFFORT", "low"),
		camera:      get("FEMI_CAMERA", "/dev/video0"),
		auth:        get("FEMI_AUTH", "login"),
		apiKey:      get("ANTHROPIC_API_KEY", ""),
		workspace:   get("ANTHROPIC_WORKSPACE_ID", ""),
		emotion:     get("FEMI_EMOTION", cartesia.DefaultEmotion),
		promptPath:  get("FEMI_PROMPT", filepath.Join(brainDir, "prompt.md")),
	}
	var err error
	if cfg.port, err = strconv.Atoi(get("FEMI_PORT", "8787")); err != nil {
		return cfg, fmt.Errorf("FEMI_PORT: %v", err)
	}
	if cfg.toolsPort, err = strconv.Atoi(get("FEMI_TOOLS_PORT", "8788")); err != nil {
		return cfg, fmt.Errorf("FEMI_TOOLS_PORT: %v", err)
	}
	ack, err := strconv.ParseFloat(get("FEMI_ACK_MINUTES", "10"), 64)
	if err != nil || ack <= 0 {
		return cfg, fmt.Errorf("FEMI_ACK_MINUTES must be a positive number of minutes")
	}
	cfg.ackWindow = time.Duration(ack * float64(time.Minute))
	switch cfg.auth {
	case "login":
	case "key":
		if cfg.apiKey == "" {
			return cfg, fmt.Errorf("FEMI_AUTH=key needs ANTHROPIC_API_KEY")
		}
	default:
		return cfg, fmt.Errorf("FEMI_AUTH must be login or key, not %q", cfg.auth)
	}
	log.Printf("config: link :%d, tools 127.0.0.1:%d, family %s, ack %s, model %s at %s effort, claude on the %s",
		cfg.port, cfg.toolsPort, cfg.familyDir, cfg.ackWindow, cfg.model, cfg.effort, cfg.auth)
	return cfg, nil
}

// readEnvFile parses KEY=VALUE lines (optional "export ", quotes, # comments).
func readEnvFile(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		out[strings.TrimSpace(k)] = v
	}
	return out
}

// lanIPs lists this machine's non-loopback IPv4 addresses as {ip, interface}.
func lanIPs() [][2]string {
	var out [][2]string
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok {
				if ip4 := ipn.IP.To4(); ip4 != nil && !ip4.IsLinkLocalUnicast() {
					out = append(out, [2]string{ip4.String(), ifc.Name})
				}
			}
		}
	}
	return out
}

// claudeEnv is what the Claude child needs on top of the brain's environment
// to run on the API key, or nothing for the login.
func (c config) claudeEnv() []string {
	if c.auth != "key" {
		return nil
	}
	env := []string{"ANTHROPIC_API_KEY=" + c.apiKey}
	if c.workspace != "" {
		env = append(env, "ANTHROPIC_CUSTOM_HEADERS=anthropic-workspace-id: "+c.workspace)
	}
	return env
}

// webcam is the camera for look_at_camera, or nil when there is none.
func (c config) webcam() femi.Camera {
	if c.camera == "none" {
		log.Printf("camera: off (FEMI_CAMERA=none)")
		return nil
	}
	if _, err := os.Stat(c.camera); err != nil {
		log.Printf("camera: %s not found, look_at_camera is off", c.camera)
		return nil
	}
	log.Printf("camera: %s (photos are never saved)", c.camera)
	return camera.Webcam{Device: c.camera}
}
