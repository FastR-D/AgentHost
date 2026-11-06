package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var lockFile *os.File

type lockInfo struct {
	Pid       int       `json:"pid"`
	Addr      string    `json:"addr"`
	StartedAt time.Time `json:"startedAt"`
}

// lockDataDir takes an exclusive lock on <data>/agenthost.lock. If another
// AgentHost holds it, it explains where that instance runs and exits.
func lockDataDir(dataDir, addr string) func() {
	path := filepath.Join(dataDir, "agenthost.lock")
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		fatal("open lock file", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		var info lockInfo
		b, _ := io.ReadAll(f)
		_ = json.Unmarshal(b, &info)
		f.Close()
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "  AgentHost 已经在运行，并且正在使用数据目录 %s\n", dataDir)
		if info.Pid > 0 {
			fmt.Fprintf(os.Stderr, "  已有实例：pid %d，地址 %s，启动于 %s\n", info.Pid, browseURL(info.Addr), info.StartedAt.Format("2006-01-02 15:04:05"))
		}
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "  · 直接使用已有实例：在浏览器打开上面的地址")
		if info.Pid > 0 {
			fmt.Fprintf(os.Stderr, "  · 重启：先停止已有实例（kill %d），再重新运行 ./start.sh\n", info.Pid)
		}
		fmt.Fprintln(os.Stderr, "  · 再开一个独立实例：换端口和数据目录，例如")
		fmt.Fprintln(os.Stderr, "      AGENTHOST_ADDR=:8081 AGENTHOST_DATA=./data2 ./start.sh")
		fmt.Fprintln(os.Stderr)
		os.Exit(1)
	}
	lockFile = f
	return func() {
		if lockFile != nil {
			_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
			lockFile.Close()
			lockFile = nil
		}
	}
}

// writeLockInfo records who holds the lock, for the message above.
func writeLockInfo(dataDir, addr string) {
	if lockFile == nil {
		return
	}
	b, _ := json.Marshal(lockInfo{Pid: os.Getpid(), Addr: addr, StartedAt: time.Now()})
	_ = lockFile.Truncate(0)
	_, _ = lockFile.WriteAt(append(b, '\n'), 0)
}

// explainListenError turns "address already in use" into actionable advice,
// telling apart another AgentHost from an unrelated program.
func explainListenError(addr string, err error) {
	if !errors.Is(err, syscall.EADDRINUSE) {
		fatal("listen", err)
	}
	_, port, _ := net.SplitHostPort(addr)
	fmt.Fprintln(os.Stderr)
	if isAgentHost(port) {
		fmt.Fprintf(os.Stderr, "  端口 %s 上已经有一个 AgentHost 在运行（使用的是另一个数据目录）：%s\n", port, browseURL(addr))
	} else {
		fmt.Fprintf(os.Stderr, "  端口 %s 已被其他程序占用。\n", port)
		fmt.Fprintf(os.Stderr, "  查看占用者：lsof -i :%s  或  ss -ltnp | grep :%s\n", port, port)
	}
	fmt.Fprintln(os.Stderr, "  换一个端口启动，例如：")
	fmt.Fprintln(os.Stderr, "      AGENTHOST_ADDR=:8081 ./start.sh")
	fmt.Fprintln(os.Stderr)
}

func isAgentHost(port string) bool {
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get("http://127.0.0.1:" + port + "/api/auth")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var v map[string]any
	if json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&v) != nil {
		return false
	}
	_, ok := v["required"]
	return ok
}

func browseURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "::" || host == "0.0.0.0" || strings.HasPrefix(host, "[") {
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port) + "/"
}
