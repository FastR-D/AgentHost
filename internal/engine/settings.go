package engine

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"agenthost/internal/node"
)

// Settings are configured from the web UI and applied to every process
// AgentHost starts, on every server, so no server needs manual setup.
type Settings struct {
	Codex     CodexSettings `json:"codex"`
	Git       GitSettings   `json:"git"`
	Runs      RunSettings   `json:"runs"`
	UpdatedAt time.Time     `json:"updatedAt"`
}

type RunSettings struct {
	RetentionDays int `json:"retentionDays"` // delete finished runs older than this; 0 = keep
}

type CodexSettings struct {
	BaseURL         string `json:"baseUrl"`         // custom provider; empty = each server's own Codex config
	APIKey          string `json:"apiKey"`          // secret
	WireAPI         string `json:"wireApi"`         // responses | chat
	Model           string `json:"model"`           // default model
	ReasoningEffort string `json:"reasoningEffort"` // "" | minimal | low | medium | high
	NetworkAccess   bool   `json:"networkAccess"`   // let agent commands use the network (git pull, npm install…)
}

type GitSettings struct {
	SSHPrivateKey string `json:"sshPrivateKey"` // secret
	SSHPublicKey  string `json:"sshPublicKey"`
	Token         string `json:"token"` // HTTPS access token, secret
	TokenUser     string `json:"tokenUser"`
	TokenHost     string `json:"tokenHost"` // e.g. github.com; empty = every host
	UserName      string `json:"userName"`
	UserEmail     string `json:"userEmail"`
}

// SettingsView is what the UI sees: secrets are never sent back.
type SettingsView struct {
	Codex struct {
		CodexSettings
		APIKeySet  bool   `json:"apiKeySet"`
		APIKeyHint string `json:"apiKeyHint,omitempty"`
	} `json:"codex"`
	Git struct {
		GitSettings
		SSHKeySet bool   `json:"sshKeySet"`
		TokenSet  bool   `json:"tokenSet"`
		TokenHint string `json:"tokenHint,omitempty"`
	} `json:"git"`
	Runs      RunSettings `json:"runs"`
	UpdatedAt time.Time   `json:"updatedAt"`
}

// SettingsPatch updates settings. A nil secret keeps the stored value; "" clears it.
type SettingsPatch struct {
	Codex *struct {
		BaseURL         *string `json:"baseUrl"`
		APIKey          *string `json:"apiKey"`
		WireAPI         *string `json:"wireApi"`
		Model           *string `json:"model"`
		ReasoningEffort *string `json:"reasoningEffort"`
		NetworkAccess   *bool   `json:"networkAccess"`
	} `json:"codex"`
	Git *struct {
		SSHPrivateKey *string `json:"sshPrivateKey"`
		Token         *string `json:"token"`
		TokenUser     *string `json:"tokenUser"`
		TokenHost     *string `json:"tokenHost"`
		UserName      *string `json:"userName"`
		UserEmail     *string `json:"userEmail"`
	} `json:"git"`
	Runs *struct {
		RetentionDays *int `json:"retentionDays"`
	} `json:"runs"`
}

type settingsStore struct {
	mu      sync.Mutex
	path    string
	s       Settings
	written map[string]string // node connection -> hash of the ssh key written there
}

func loadSettings(dataDir string) *settingsStore {
	st := &settingsStore{path: filepath.Join(dataDir, "settings.json"), written: map[string]string{}}
	if b, err := os.ReadFile(st.path); err == nil {
		_ = json.Unmarshal(b, &st.s)
	}
	return st
}

func (st *settingsStore) get() Settings {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.s
}

func hint(s string) string {
	if s == "" {
		return ""
	}
	r := []rune(s)
	if len(r) <= 8 {
		return "••••"
	}
	return string(r[:3]) + "…" + string(r[len(r)-4:])
}

func viewOf(s Settings) SettingsView {
	var v SettingsView
	v.Codex.CodexSettings = s.Codex
	v.Codex.APIKey = ""
	v.Codex.APIKeySet, v.Codex.APIKeyHint = s.Codex.APIKey != "", hint(s.Codex.APIKey)
	v.Git.GitSettings = s.Git
	v.Git.SSHPrivateKey, v.Git.Token = "", ""
	v.Git.SSHKeySet = s.Git.SSHPrivateKey != ""
	v.Git.TokenSet, v.Git.TokenHint = s.Git.Token != "", hint(s.Git.Token)
	v.Runs = s.Runs
	v.UpdatedAt = s.UpdatedAt
	return v
}

func (e *Engine) GetSettings() SettingsView { return viewOf(e.settings.get()) }

var reHost = regexp.MustCompile(`^[A-Za-z0-9.-]+(:\d+)?$`)

func (e *Engine) UpdateSettings(p SettingsPatch) (SettingsView, error) {
	st := e.settings
	st.mu.Lock()
	n := st.s
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = strings.TrimSpace(*v)
		}
	}
	if c := p.Codex; c != nil {
		set(&n.Codex.BaseURL, c.BaseURL)
		set(&n.Codex.APIKey, c.APIKey)
		set(&n.Codex.WireAPI, c.WireAPI)
		set(&n.Codex.Model, c.Model)
		set(&n.Codex.ReasoningEffort, c.ReasoningEffort)
		if c.NetworkAccess != nil {
			n.Codex.NetworkAccess = *c.NetworkAccess
		}
	}
	if g := p.Git; g != nil {
		if g.SSHPrivateKey != nil {
			k := strings.TrimSpace(*g.SSHPrivateKey)
			n.Git.SSHPrivateKey, n.Git.SSHPublicKey = "", ""
			if k != "" {
				if !strings.Contains(k, "PRIVATE KEY-----") {
					st.mu.Unlock()
					return SettingsView{}, errors.New("这不是私钥：请粘贴以 -----BEGIN … PRIVATE KEY----- 开头的完整内容，或者直接点“生成新密钥”")
				}
				n.Git.SSHPrivateKey = k + "\n"
				n.Git.SSHPublicKey = publicKeyOf(n.Git.SSHPrivateKey)
			}
		}
		set(&n.Git.Token, g.Token)
		set(&n.Git.TokenUser, g.TokenUser)
		set(&n.Git.TokenHost, g.TokenHost)
		set(&n.Git.UserName, g.UserName)
		set(&n.Git.UserEmail, g.UserEmail)
	}
	if r := p.Runs; r != nil && r.RetentionDays != nil {
		if *r.RetentionDays < 0 || *r.RetentionDays > 3650 {
			st.mu.Unlock()
			return SettingsView{}, errors.New("保留天数只能是 0（不自动清理）到 3650")
		}
		n.Runs.RetentionDays = *r.RetentionDays
	}
	if n.Codex.BaseURL != "" && !strings.HasPrefix(n.Codex.BaseURL, "http://") && !strings.HasPrefix(n.Codex.BaseURL, "https://") {
		st.mu.Unlock()
		return SettingsView{}, errors.New("Codex 接口地址需要以 http:// 或 https:// 开头，例如 https://api.openai.com/v1")
	}
	switch n.Codex.WireAPI {
	case "", "responses", "chat":
	default:
		st.mu.Unlock()
		return SettingsView{}, errors.New("接口协议只能是 responses 或 chat")
	}
	switch n.Codex.ReasoningEffort {
	case "", "minimal", "low", "medium", "high", "xhigh":
	default:
		st.mu.Unlock()
		return SettingsView{}, errors.New("推理强度只能是 minimal / low / medium / high")
	}
	n.Git.TokenHost = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(n.Git.TokenHost, "https://"), "http://"), "/")
	if n.Git.TokenHost != "" && !reHost.MatchString(n.Git.TokenHost) {
		st.mu.Unlock()
		return SettingsView{}, errors.New("Git 主机名格式不对，例如 github.com 或 git.example.com:8443")
	}
	n.UpdatedAt = time.Now()
	b, _ := json.MarshalIndent(n, "", "  ")
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		st.mu.Unlock()
		return SettingsView{}, err
	}
	if err := os.Rename(tmp, st.path); err != nil {
		st.mu.Unlock()
		return SettingsView{}, err
	}
	st.s = n
	st.mu.Unlock()
	e.hub.Publish("settings", viewOf(n))
	return viewOf(n), nil
}

// GenerateSSHKey creates a new ed25519 key pair for git and stores it.
func (e *Engine) GenerateSSHKey() (SettingsView, error) {
	priv, pub, err := newSSHKey("agenthost")
	if err != nil {
		return SettingsView{}, err
	}
	v, err := e.UpdateSettings(SettingsPatch{Git: &struct {
		SSHPrivateKey *string `json:"sshPrivateKey"`
		Token         *string `json:"token"`
		TokenUser     *string `json:"tokenUser"`
		TokenHost     *string `json:"tokenHost"`
		UserName      *string `json:"userName"`
		UserEmail     *string `json:"userEmail"`
	}{SSHPrivateKey: &priv}})
	if err != nil {
		return v, err
	}
	// newSSHKey knows the public key even where ssh-keygen is missing.
	e.settings.mu.Lock()
	e.settings.s.Git.SSHPublicKey = pub
	n := e.settings.s
	e.settings.mu.Unlock()
	b, _ := json.MarshalIndent(n, "", "  ")
	_ = os.WriteFile(e.settings.path, b, 0o600)
	return viewOf(n), nil
}

func (e *Engine) defaultModel() string {
	return firstNonEmpty(e.settings.get().Codex.Model, e.cfg.DefaultModel)
}

// ---------------------------------------------------------------- applying settings

const codexKeyEnv = "AGENTHOST_CODEX_API_KEY"

func tomlString(s string) string { return strconv.Quote(s) }

// codexArgs are the `codex -c` overrides used on a node.
func (e *Engine) codexArgs(nd node.Node) []string {
	return e.codexArgsFor(nd, false)
}

func (e *Engine) codexArgsFor(nd node.Node, network bool, writable ...string) []string {
	s := e.settings.get().Codex
	args := append([]string{}, e.cfg.CodexArgs...)
	if s.BaseURL != "" {
		wire := firstNonEmpty(s.WireAPI, "responses")
		prov := fmt.Sprintf(`{name="AgentHost", base_url=%s, wire_api=%s, requires_openai_auth=false`, tomlString(s.BaseURL), tomlString(wire))
		if s.APIKey != "" {
			prov += `, env_key="` + codexKeyEnv + `"`
		}
		prov += "}"
		args = append(args, "-c", `model_provider="agenthost"`, "-c", "model_providers.agenthost="+prov)
	}
	if s.Model != "" {
		args = append(args, "-c", "model="+tomlString(s.Model))
	}
	if s.ReasoningEffort != "" {
		args = append(args, "-c", "model_reasoning_effort="+tomlString(s.ReasoningEffort))
	}
	if s.NetworkAccess || network {
		args = append(args, "-c", "sandbox_workspace_write.network_access=true")
	}
	if len(writable) > 0 {
		q := make([]string, len(writable))
		for i, w := range writable {
			q[i] = tomlString(w)
		}
		args = append(args, "-c", "sandbox_workspace_write.writable_roots=["+strings.Join(q, ", ")+"]")
	}
	return args
}

// extraEnv is added to every process started on a node: the Codex API key and
// git credentials. It may write the SSH key file to the node, so do not call
// it with e.mu held.
func (e *Engine) extraEnv(nd node.Node) []string {
	s := e.settings.get()
	var env []string
	if s.Codex.APIKey != "" {
		env = append(env, codexKeyEnv+"="+s.Codex.APIKey)
	}
	g := s.Git
	if g.UserName != "" {
		env = append(env, "GIT_AUTHOR_NAME="+g.UserName, "GIT_COMMITTER_NAME="+g.UserName)
	}
	if g.UserEmail != "" {
		env = append(env, "GIT_AUTHOR_EMAIL="+g.UserEmail, "GIT_COMMITTER_EMAIL="+g.UserEmail)
	}
	var cfg [][2]string
	if g.Token != "" {
		key := "credential.helper"
		if g.TokenHost != "" {
			key = "credential.https://" + g.TokenHost + ".helper"
		}
		// git asks helpers in order and the first answer wins, so first reset
		// the list (an empty value does that) to keep other helpers on the
		// server from shadowing this token. The variable names avoid
		// KEY/TOKEN/SECRET, which Codex strips from agent commands by default.
		cfg = append(cfg, [2]string{key, ""},
			[2]string{key, `!f() { test "$1" = get || exit 0; echo "username=${AGENTHOST_GIT_USER}"; echo "password=${AGENTHOST_GIT_PASS}"; }; f`})
		env = append(env, "AGENTHOST_GIT_USER="+firstNonEmpty(g.TokenUser, "x-access-token"), "AGENTHOST_GIT_PASS="+g.Token)
	}
	if g.SSHPrivateKey != "" {
		if path, err := e.ensureKeyFile(nd, g.SSHPrivateKey); err == nil {
			known := filepath.Join(filepath.Dir(path), "known_hosts")
			env = append(env, "GIT_SSH_COMMAND=ssh -i "+shellQuote(path)+" -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile="+shellQuote(known))
		}
	}
	if len(cfg) > 0 {
		env = append(env, "GIT_CONFIG_COUNT="+strconv.Itoa(len(cfg)))
		for i, kv := range cfg {
			env = append(env, fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", i, kv[0]), fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, kv[1]))
		}
	}
	return env
}

func shellQuote(s string) string {
	if regexp.MustCompile(`^[A-Za-z0-9_./-]+$`).MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ensureKeyFile puts the SSH private key on the node (once per connection and key).
func (e *Engine) ensureKeyFile(nd node.Node, key string) (string, error) {
	info := nd.Info()
	base := info.DataDir
	if info.Local {
		base = e.cfg.DataDir
	}
	if base == "" {
		return "", errors.New("服务器没有报告数据目录（请升级 agenthost node）")
	}
	path := filepath.Join(base, "private", "git_ssh_key")
	h := sha256.Sum256([]byte(key))
	hash := hex.EncodeToString(h[:])
	conn := info.ID + "@" + info.ConnectedAt.String()
	e.settings.mu.Lock()
	done := e.settings.written[conn] == hash
	e.settings.mu.Unlock()
	if done {
		return path, nil
	}
	if err := nd.WriteFile(path, []byte(key), 0o600); err != nil {
		return "", err
	}
	e.settings.mu.Lock()
	e.settings.written[conn] = hash
	e.settings.mu.Unlock()
	return path, nil
}

// ---------------------------------------------------------------- checks from the UI

// CheckResult is the outcome of a connectivity check.
type CheckResult struct {
	OK     bool   `json:"ok"`
	Output string `json:"output"`
}

func (e *Engine) runCheck(ctx context.Context, nd node.Node, spec node.Spec) (int, string, error) {
	return e.runScript(ctx, nd, spec, 64*1024)
}

// runScript runs a process on a node and returns its exit code and up to
// maxOut bytes of combined output.
func (e *Engine) runScript(ctx context.Context, nd node.Node, spec node.Spec, maxOut int) (int, string, error) {
	p, err := nd.Start(spec)
	if err != nil {
		return -1, "", err
	}
	// Checks never read stdin: close it so tools like `codex exec` don't wait for input.
	_ = p.Stdin().Close()
	var out strings.Builder
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, r := range []io.Reader{p.Stdout(), p.Stderr()} {
		wg.Add(1)
		go func(r io.Reader) {
			defer wg.Done()
			buf := make([]byte, 8192)
			for {
				n, err := r.Read(buf)
				mu.Lock()
				if out.Len() < maxOut {
					out.Write(buf[:n])
				}
				mu.Unlock()
				if err != nil {
					return
				}
			}
		}(r)
	}
	done := make(chan struct{})
	var code int
	var werr error
	go func() { wg.Wait(); code, werr = p.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		p.Signal(15)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			p.Signal(9)
			<-done
		}
		return -1, out.String(), errors.New("超时")
	}
	return code, out.String(), werr
}

// TestGit runs `git ls-remote` against a repository on a server with the configured credentials.
func (e *Engine) TestGit(nodeID, repo string) (CheckResult, error) {
	repo = strings.TrimSpace(repo)
	if repo == "" || strings.HasPrefix(repo, "-") {
		return CheckResult{}, errors.New("请填写仓库地址，例如 git@github.com:me/repo.git")
	}
	nd, err := e.node(nodeID)
	if err != nil {
		return CheckResult{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	env := append(e.extraEnv(nd), "GIT_TERMINAL_PROMPT=0")
	code, out, err := e.runCheck(ctx, nd, node.Spec{Script: "git ls-remote --heads -- " + shellQuote(repo) + " | head -20", Dir: workspaceOf(nd), Env: env})
	if err != nil {
		return CheckResult{OK: false, Output: strings.TrimSpace(out + "\n" + err.Error())}, nil
	}
	// The pipeline's status is head's; detect git failures from the output.
	failed := code != 0 || strings.Contains(out, "fatal:") || strings.Contains(out, "Permission denied") || strings.TrimSpace(out) == ""
	return CheckResult{OK: !failed, Output: strings.TrimSpace(out)}, nil
}

// TestCodex asks Codex on a server for a one-word reply using the configured provider.
func (e *Engine) TestCodex(nodeID string) (CheckResult, error) {
	nd, err := e.node(nodeID)
	if err != nil {
		return CheckResult{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	argv := append([]string{e.codexBin(nd), "exec"}, e.codexArgs(nd)...)
	argv = append(argv, "--skip-git-repo-check", "--ephemeral", "--json", "--sandbox", "read-only", "只回复 ok 两个字母")
	code, out, err := e.runCheck(ctx, nd, node.Spec{Argv: argv, Dir: workspaceOf(nd), Env: e.extraEnv(nd)})
	if err != nil && code == -1 && out == "" {
		return CheckResult{OK: false, Output: err.Error()}, nil
	}
	reply, lastErr := "", ""
	for _, l := range strings.Split(out, "\n") {
		var m struct {
			Type string `json:"type"`
			Item struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
			Message string `json:"message"`
			Error   struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(l), &m) != nil {
			continue
		}
		switch {
		case m.Type == "item.completed" && m.Item.Type == "agent_message":
			reply = m.Item.Text
		case m.Type == "turn.failed":
			lastErr = m.Error.Message
		case m.Type == "error" && !strings.Contains(m.Message, "unrecognized configuration"):
			lastErr = m.Message
		}
	}
	if reply != "" {
		return CheckResult{OK: true, Output: "Codex 回复：" + reply}, nil
	}
	if lastErr == "" {
		lastErr = truncate(strings.TrimSpace(out), 2000)
	}
	return CheckResult{OK: false, Output: lastErr}, nil
}

// ---------------------------------------------------------------- ssh keys

func sshString(b []byte) []byte {
	out := make([]byte, 4+len(b))
	binary.BigEndian.PutUint32(out, uint32(len(b)))
	copy(out[4:], b)
	return out
}

// newSSHKey returns an OpenSSH-format ed25519 private key and its public key line.
func newSSHKey(comment string) (string, string, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	blob := append(sshString([]byte("ssh-ed25519")), sshString(pub)...)
	var check [4]byte
	if _, err := rand.Read(check[:]); err != nil {
		return "", "", err
	}
	var block []byte
	block = append(block, check[:]...)
	block = append(block, check[:]...)
	block = append(block, sshString([]byte("ssh-ed25519"))...)
	block = append(block, sshString(pub)...)
	block = append(block, sshString(priv)...) // 64 bytes: seed + public key
	block = append(block, sshString([]byte(comment))...)
	for i := byte(1); len(block)%8 != 0; i++ {
		block = append(block, i)
	}
	var body []byte
	body = append(body, []byte("openssh-key-v1\x00")...)
	body = append(body, sshString([]byte("none"))...)
	body = append(body, sshString([]byte("none"))...)
	body = append(body, sshString(nil)...)
	body = append(body, 0, 0, 0, 1)
	body = append(body, sshString(blob)...)
	body = append(body, sshString(block)...)
	enc := base64.StdEncoding.EncodeToString(body)
	var b strings.Builder
	b.WriteString("-----BEGIN OPENSSH PRIVATE KEY-----\n")
	for len(enc) > 70 {
		b.WriteString(enc[:70] + "\n")
		enc = enc[70:]
	}
	b.WriteString(enc + "\n-----END OPENSSH PRIVATE KEY-----\n")
	return b.String(), "ssh-ed25519 " + base64.StdEncoding.EncodeToString(blob) + " " + comment, nil
}

// publicKeyOf extracts the public key from an unencrypted OpenSSH private key.
func publicKeyOf(priv string) string {
	s := priv
	i := strings.Index(s, "-----BEGIN OPENSSH PRIVATE KEY-----")
	j := strings.Index(s, "-----END OPENSSH PRIVATE KEY-----")
	if i < 0 || j < i {
		return "" // other formats (PEM RSA…) still work, the public key is just not shown
	}
	data, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(s[i+35:j]), ""))
	if err != nil || !strings.HasPrefix(string(data), "openssh-key-v1\x00") {
		return ""
	}
	rest := data[len("openssh-key-v1\x00"):]
	read := func() ([]byte, bool) {
		if len(rest) < 4 {
			return nil, false
		}
		n := binary.BigEndian.Uint32(rest)
		if uint32(len(rest)-4) < n {
			return nil, false
		}
		v := rest[4 : 4+n]
		rest = rest[4+n:]
		return v, true
	}
	for k := 0; k < 3; k++ { // cipher, kdf, kdf options
		if _, ok := read(); !ok {
			return ""
		}
	}
	if len(rest) < 4 {
		return ""
	}
	rest = rest[4:]
	blob, ok := read()
	if !ok || len(blob) < 4 {
		return ""
	}
	n := binary.BigEndian.Uint32(blob)
	if uint32(len(blob)-4) < n {
		return ""
	}
	return string(blob[4:4+n]) + " " + base64.StdEncoding.EncodeToString(blob)
}
