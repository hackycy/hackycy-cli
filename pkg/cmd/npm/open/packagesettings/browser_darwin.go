package packagesettings

import (
	"context"
	"os/exec"
)

func openURL(ctx context.Context, address string) error {
	return contextError(ctx, exec.CommandContext(ctx, "open", address).Run())
}
