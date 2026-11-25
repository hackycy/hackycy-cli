//go:build !darwin && !linux && !windows

package packagesettings

import (
	"context"
	"errors"
)

func openURL(context.Context, string) error {
	return errors.New("opening URLs is not supported on this platform")
}
