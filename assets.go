// Package agenthost holds files embedded into the AgentHost binary.
package agenthost

import (
	"embed"
	"io/fs"
)

//go:embed all:web/dist
var webDist embed.FS

//go:embed tasks
var tasks embed.FS

// WebUI returns the built web UI, or nil if it was not built before compiling.
func WebUI() fs.FS {
	sub, err := fs.Sub(webDist, "web/dist")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}

// ExampleTasks are copied into a new task library.
func ExampleTasks() fs.FS {
	sub, _ := fs.Sub(tasks, "tasks")
	return sub
}
