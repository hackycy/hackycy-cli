package node

import (
	"bytes"
	"context"

	nodeent "github.com/hackycy/hackycy-cli/ent/node"
)

func controllerMatchesNodeV1(ctx context.Context, client *nodeent.Client, controllerPublic []byte) (bool, error) {
	binding, err := client.ControllerBinding.Get(ctx, 1)
	if nodeent.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return bytes.Equal(binding.ControllerPublic, controllerPublic), nil
}
