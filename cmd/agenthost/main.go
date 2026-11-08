// Command agenthost runs the AgentHost server: a remote web console that
// hands messages and Markdown tasks to the Codex CLI.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
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

	"agenthost"
	"agenthost/internal/engine"
	"agenthost/internal/node"
	"agenthost/internal/server"
)

const version = "0.2.1"

// build metadata injected by -ldflags
var (
	buildCommit = "dev"
	buildDate   = "unknown"
)

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "node":
			os.Exit(runNode(os.Args[2:]))
		}
	}
	addr := flag.String("addr", env("AGENTHOST_ADDR", "0.0.0.0:8080"), "listen address (AGENTHOST_ADDR)")
	dataDir := flag.String("data", env("AGENTHOST_DATA", "./data"), "data directory (AGENTHOST_DATA)")
	workspace := flag.String("workspace", env("AGENTHOST_WORKSPACE", "./workspace"), "default working directory for tasks (AGENTHOST_WORKSPACE)")
	webDir := flag.String("web", env("AGENTHOST_WEB_DIR", ""), "serve the web UI from this directory instead of the embedded build")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	abs := func(p string) string {
		a, err := filepath.Abs(p)
		if err != nil {
			fatal("bad path", err)
		}
		return a
	}
	*dataDir, *workspace = abs(*dataDir), abs(*workspace)
	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		fatal("create data dir", err)
	}

	// Lock the data dir and bind the port before loading any state, so a
	// second instance can neither corrupt the first one's data nor half-start.
	unlock := lockDataDir(*dataDir, *addr)
	defer unlock()
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		explainListenError(*addr, err)
		unlock()
		os.Exit(1)
	}
	writeLockInfo(*dataDir, ln.Addr().String())

	codexBin := env("CODEX_BIN", "codex")
	var codexArgs []string
	for _, kv := range strings.Split(os.Getenv("AGENTHOST_CODEX_CONFIG"), ";") {
		if kv = strings.TrimSpace(kv); kv != "" {
			codexArgs = append(codexArgs, "-c", kv)
		}
	}
	maxRuns, _ := strconv.Atoi(env("AGENTHOST_MAX_RUNS", "4"))

	tasks, err := engine.OpenTaskLib(filepath.Join(*dataDir, "tasks"), agenthost.ExampleTasks())
	if err != nil {
		fatal("open task library", err)
	}
	codexVersion := detectCodex(codexBin)
	var local *node.Local
	if !isOff(os.Getenv("AGENTHOST_LOCAL_NODE")) {
		local = node.NewLocal(nodeInfo(env("AGENTHOST_LOCAL_NAME", "本机"), *workspace, *dataDir, codexBin, codexVersion))
	} else {
		slog.Info("this web server does not run tasks itself (AGENTHOST_LOCAL_NODE=off); connect servers with `agenthost node`")
	}
	nodes := node.NewRegistry(local, *dataDir, resolveSecret(*dataDir, "AGENTHOST_NODE_TOKEN", "node-token"))

	cfg := engine.Config{
		Nodes:             nodes,
		Tasks:             tasks,
		DataDir:           *dataDir,
		Workspace:         *workspace,
		CodexBin:          codexBin,
		CodexArgs:         codexArgs,
		DefaultModel:      env("AGENTHOST_MODEL", ""),
		ApprovalDecision:  env("AGENTHOST_APPROVAL_DECISION", "decline"),
		ReasoningSummary:  env("AGENTHOST_REASONING_SUMMARY", "auto"),
		MaxConcurrentRuns: maxRuns,
	}
	if cfg.ReasoningSummary == "default" || cfg.ReasoningSummary == "off" {
		cfg.ReasoningSummary = ""
	}
	eng, err := engine.New(cfg)
	if err != nil {
		fatal("start engine", err)
	}
	nodes.OnChange(func() { eng.Hub().Publish("nodes", nodes.List()) })

	token := resolveToken(*dataDir)

	var static fs.FS
	if *webDir != "" {
		static = os.DirFS(*webDir)
	} else {
		static = agenthost.WebUI()
		if static == nil {
			slog.Warn("web UI is not embedded in this binary; run start.sh (or `npm run build` in web/) before `go build`")
		}
	}

	info := map[string]any{
		"version":        version + "-" + buildCommit,
		"buildDate":      buildDate,
		"codexBin":       codexBin,
		"codexVersion":   codexVersion,
		"codexArgs":      codexArgs,
		"workspace":      *workspace,
		"dataDir":        *dataDir,
		"defaultModel":   cfg.DefaultModel,
		"defaultSandbox": engine.CodexSandbox,
		"approvalPolicy": engine.CodexApproval,
		"maxRuns":        maxRuns,
		"authEnabled":    token != "",
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           server.New(eng, token, static, info),
		ReadHeaderTimeout: 15 * time.Second,
	}
	slog.Info("AgentHost started", "addr", ln.Addr().String(), "workspace", *workspace, "data", *dataDir, "codex", codexVersion)
	printBanner(ln.Addr().String(), token)

	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fatal("serve", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	slog.Info("shutting down: stopping running tasks")
	eng.StopAll(20 * time.Second)
	nodes.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

// resolveToken returns the access token: AGENTHOST_TOKEN, or a generated
// token persisted in the data dir. AGENTHOST_AUTH=none disables auth.
func resolveToken(dataDir string) string {
	if strings.EqualFold(os.Getenv("AGENTHOST_AUTH"), "none") {
		slog.Warn("authentication is DISABLED (AGENTHOST_AUTH=none); anyone who can reach this port can run commands")
		return ""
	}
	return resolveSecret(dataDir, "AGENTHOST_TOKEN", "token")
}

// resolveSecret returns $envName, or a random secret persisted in dataDir/file.
func resolveSecret(dataDir, envName, file string) string {
	if t := strings.TrimSpace(os.Getenv(envName)); t != "" {
		return t
	}
	path := filepath.Join(dataDir, file)
	if b, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(b)) != "" {
		return strings.TrimSpace(string(b))
	}
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	t := hex.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(t+"\n"), 0o600); err != nil {
		slog.Warn("could not persist generated secret", "file", path, "err", err)
	}
	return t
}

func isOff(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "off", "false", "0", "no", "none", "disable", "disabled":
		return true
	}
	return false
}

func detectCodex(bin string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		slog.Warn("codex CLI not found or not runnable; tasks will fail until it is installed", "bin", bin, "err", err)
		return "unavailable"
	}
	return strings.TrimSpace(string(out))
}

func printBanner(addr, token string) {
	host := addr
	if strings.HasPrefix(addr, "[::]") || strings.HasPrefix(addr, "0.0.0.0") || strings.HasPrefix(addr, ":") {
		_, port, _ := net.SplitHostPort(addr)
		if ip := outboundIP(); ip != "" {
			host = net.JoinHostPort(ip, port)
		} else {
			host = "localhost:" + port
		}
	}
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  AgentHost is running.")
	if token != "" {
		fmt.Fprintf(os.Stderr, "  Open:  http://%s/?token=%s\n", host, token)
		fmt.Fprintf(os.Stderr, "  Token: %s\n", token)
	} else {
		fmt.Fprintf(os.Stderr, "  Open:  http://%s/\n", host)
	}
	fmt.Fprintln(os.Stderr, "")
}

// outboundIP returns the machine's preferred outbound IP address.
// Tries multiple methods in order: public IP API, UDP route, local interfaces.
func outboundIP() string {
	// 1. UDP connect to get the outbound interface IP (fast, no network needed)
	if conn, err := net.Dial("udp", "8.8.8.8:80"); err == nil {
		ip := conn.LocalAddr().(*net.UDPAddr).IP.String()
		conn.Close()
		if !isPrivateIP(ip) {
			return ip // already a public IP
		}
		// It's a private IP (NAT/cloud): try to get public IP from external API
		if pub := publicIP(); pub != "" {
			return pub
		}
		return ip // return the private IP rather than localhost
	}

	// 2. Walk local interfaces, pick the first non-loopback unicast IP
	if ifaces, err := net.Interfaces(); err == nil {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, _ := iface.Addrs()
			for _, addr := range addrs {
				var ip net.IP
				switch v := addr.(type) {
				case *net.IPNet:
					ip = v.IP
				case *net.IPAddr:
					ip = v.IP
				}
				if ip == nil || ip.IsLoopback() || ip.To4() == nil {
					continue
				}
				return ip.String()
			}
		}
	}

	// 3. Try external API as last resort
	return publicIP()
}

// publicIP queries a lightweight public endpoint for the machine's external IP.
func publicIP() string {
	for _, u := range []string{"https://api4.ipify.org", "https://ipv4.icanhazip.com"} {
		c := &http.Client{Timeout: 3 * time.Second}
		resp, err := c.Get(u)
		if err != nil {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, 64))
		resp.Body.Close()
		if err == nil {
			if ip := strings.TrimSpace(string(b)); ip != "" {
				return ip
			}
		}
	}
	return ""
}

func isPrivateIP(ip string) bool {
	for _, p := range []string{
		"10.", "127.", "169.254.", "192.168.",
		"172.16.", "172.17.", "172.18.", "172.19.", "172.20.", "172.21.", "172.22.", "172.23.",
		"172.24.", "172.25.", "172.26.", "172.27.", "172.28.", "172.29.", "172.30.", "172.31.",
		"fc", "fd",
	} {
		if strings.HasPrefix(ip, p) {
			return true
		}
	}
	return false
}

func fatal(msg string, err error) {
	slog.Error(msg, "err", err)
	os.Exit(1)
}
