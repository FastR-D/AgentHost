package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"agenthost/internal/node"
)

func nodeInfo(name, workspace, dataDir, codexBin, codexVersion string) node.Info {
	host, _ := os.Hostname()
	return node.Info{
		Name: name, Hostname: host, OS: runtime.GOOS, Arch: runtime.GOARCH, Version: version,
		CodexBin: codexBin, CodexVersion: codexVersion, Workspace: workspace, DataDir: dataDir,
	}
}

// runNode implements `agenthost node`: this machine runs tasks for a web
// server it dials out to, so it needs no public address or open port.
func runNode(args []string) int {
	fs := flag.NewFlagSet("node", flag.ExitOnError)
	host, _ := os.Hostname()
	hub := fs.String("hub", env("AGENTHOST_HUB", ""), "web server URL, e.g. https://agenthost.example.com (AGENTHOST_HUB)")
	token := fs.String("token", env("AGENTHOST_NODE_TOKEN", ""), "node token from the web server's 服务器 page (AGENTHOST_NODE_TOKEN)")
	name := fs.String("name", env("AGENTHOST_NODE_NAME", host), "name shown in the server list (AGENTHOST_NODE_NAME)")
	workspace := fs.String("workspace", env("AGENTHOST_WORKSPACE", "./workspace"), "working directory root for tasks on this server (AGENTHOST_WORKSPACE)")
	dataDir := fs.String("data", env("AGENTHOST_DATA", "./node-data"), "private data of this node, e.g. the git key (AGENTHOST_DATA)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "用法：agenthost node --hub <Web 服务器地址> --token <节点令牌> [--name 名称] [--workspace 目录]")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if *hub == "" || *token == "" {
		fs.Usage()
		return 2
	}
	ws, err := filepath.Abs(*workspace)
	if err != nil {
		fatal("bad workspace", err)
	}
	dd, err := filepath.Abs(*dataDir)
	if err != nil {
		fatal("bad data dir", err)
	}
	for _, d := range []string{ws, dd} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			fatal("create dir", err)
		}
	}
	codexBin := env("CODEX_BIN", "codex")
	local := node.NewLocal(nodeInfo(*name, ws, dd, codexBin, detectCodex(codexBin)))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	slog.Info("AgentHost node starting", "name", *name, "hub", *hub, "workspace", ws)
	err = node.RunAgent(ctx, node.AgentConfig{
		Hub: *hub, Token: *token, Local: local,
		Logf: func(format string, a ...any) { slog.Info(fmt.Sprintf(format, a...)) },
	})
	if err != nil {
		slog.Error("node stopped", "err", err)
		return 1
	}
	return 0
}
