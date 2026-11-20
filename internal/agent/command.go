package agent

import (
	"context"
	"fmt"
	"os/exec"
)

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, command string, arguments ...string) error {
	output, err := exec.CommandContext(ctx, command, arguments...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, output)
	}
	return nil
}
