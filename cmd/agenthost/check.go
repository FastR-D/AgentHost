package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"agenthost/internal/workflow"
)

// runCheck implements `agenthost check FILE...`: print how each workflow is
// understood, or where it fails. Used by people and by the Codex skill.
func runCheck(files []string) int {
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "用法：agenthost check <workflow.md>...（文件名为 - 时从标准输入读取）")
		return 2
	}
	code := 0
	for i, f := range files {
		var b []byte
		var err error
		if f == "-" {
			b, err = io.ReadAll(os.Stdin)
		} else {
			b, err = os.ReadFile(f)
		}
		if i > 0 {
			fmt.Println()
		}
		if len(files) > 1 {
			fmt.Printf("== %s\n", f)
		}
		if err != nil {
			fmt.Printf("✗ %v\n", err)
			code = 1
			continue
		}
		wf, err := workflow.Parse(string(b), strings.TrimSuffix(filepath.Base(f), ".md"))
		if err != nil {
			fmt.Printf("✗ %v\n", err)
			code = 1
			continue
		}
		fmt.Print("✓ " + workflow.Describe(wf))
	}
	return code
}
