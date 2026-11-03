package translate

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"os/exec"
)

// cliBackend translates through the signed-in Claude Code CLI (`claude -p`),
// so no API key is needed: the user's Claude subscription pays for it. The
// document goes in on stdin, the instructions as the system prompt, and no
// tools are offered, so the call is a single model turn.
type cliBackend struct {
	path string
}

// cliModel is the Claude Code alias for the current Opus, which is Model.
const cliModel = "opus"

func (b *cliBackend) translate(ctx context.Context, system, markdown string) (string, error) {
	cmd := exec.CommandContext(ctx, b.path,
		"-p",
		"--model", cliModel,
		"--effort", "high",
		"--no-session-persistence",
		"--tools", "",
		"--output-format", "text",
		"--system-prompt", system,
	)
	cmd.Stdin = strings.NewReader(markdown)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		return "", fmt.Errorf("claude -p: %w: %s", err, detail)
	}
	return stdout.String(), nil
}
