package node

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	nodeent "github.com/hackycy/hackycy-cli/ent/node"
	"github.com/ncruces/go-sqlite3"
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

func claimNodeV1(ctx context.Context, client *nodeent.Client, controllerPublic []byte) (bool, error) {
	if len(controllerPublic) != 32 {
		return false, fmt.Errorf("invalid Controller identity")
	}
	tx, err := client.Tx(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := tx.ControllerBinding.Create().SetID(1).SetControllerPublic(controllerPublic).Save(ctx); err != nil {
		if errors.Is(err, sqlite3.CONSTRAINT_PRIMARYKEY) {
			if _, lookupErr := tx.ControllerBinding.Get(ctx, 1); lookupErr == nil {
				return false, nil
			} else {
				return false, lookupErr
			}
		}
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
