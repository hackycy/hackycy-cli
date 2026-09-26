package server

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	serverent "github.com/hackycy/hackycy-cli/ent/server"
	"github.com/hackycy/hackycy-cli/ent/server/nodeportpool"
	"github.com/hackycy/hackycy-cli/ent/server/tunnel"
)

func syncLocalNodeProjection(ctx context.Context, database *sql.DB, settings ServerHTTPServerSettings) error {
	pool := settings.PortRange
	if pool.Start < 1 || pool.End > 65535 || pool.Start > pool.End {
		return fmt.Errorf("Local Node port pool is invalid")
	}
	for _, reserved := range []int{settings.ControlPort, settings.FRPPort, settings.HTTPPort} {
		if reserved >= pool.Start && reserved <= pool.End {
			return fmt.Errorf("Local Node port pool includes listener port %d", reserved)
		}
	}
	_, err := withImmediateTransaction(ctx, database, func(connection *sql.Conn) (struct{}, error) {
		client := serverEntOnConnection(connection)
		occupied, err := client.Tunnel.Query().Where(
			tunnel.NodeIDEQ("local"), tunnel.ProtocolIn(tunnel.ProtocolTCP, tunnel.ProtocolUDP),
			tunnel.Or(tunnel.ServerPortLT(int(pool.Start)), tunnel.ServerPortGT(int(pool.End))),
		).First(ctx)
		if err == nil {
			return struct{}{}, fmt.Errorf("Local Node port pool %d-%d excludes occupied port %d", pool.Start, pool.End, *occupied.ServerPort)
		}
		if !serverent.IsNotFound(err) {
			return struct{}{}, fmt.Errorf("check Local Node port pool: %w", err)
		}
		var frpHost, httpHost string
		var frpPort, httpPort int
		if settings.AdvertiseFRPAddr != nil {
			frpHost = strings.ToLower(settings.AdvertiseFRPAddr.Host)
			frpPort = int(settings.AdvertiseFRPAddr.Port)
			httpHost = frpHost
			httpPort = int(settings.HTTPPort)
		} else if settings.Address != "" && settings.Address != "0.0.0.0" && settings.Address != "::" {
			frpHost = strings.ToLower(settings.Address)
			frpPort = int(settings.FRPPort)
			httpHost = frpHost
			httpPort = int(settings.HTTPPort)
		}
		update := client.Node.UpdateOneID("local").SetUpdatedAt(time.Now().UTC().Format("2006-01-02 15:04:05"))
		if frpHost == "" {
			update.ClearAdvertisedFrpHost().ClearAdvertisedFrpPort().ClearHTTPIngressHost().ClearHTTPIngressPort()
		} else {
			update.SetAdvertisedFrpHost(frpHost).SetAdvertisedFrpPort(frpPort).
				SetHTTPIngressHost(httpHost).SetHTTPIngressPort(httpPort)
		}
		if _, err := update.Save(ctx); err != nil {
			return struct{}{}, fmt.Errorf("project Local Node endpoint: %w", err)
		}
		currentPool, err := client.NodePortPool.Query().Where(nodeportpool.NodeIDEQ("local")).Only(ctx)
		if serverent.IsNotFound(err) {
			_, err = client.NodePortPool.Create().SetNodeID("local").SetPortStart(int(pool.Start)).SetPortEnd(int(pool.End)).Save(ctx)
		} else if err == nil {
			_, err = client.NodePortPool.UpdateOne(currentPool).SetPortStart(int(pool.Start)).SetPortEnd(int(pool.End)).Save(ctx)
		}
		if err != nil {
			return struct{}{}, fmt.Errorf("project Local Node port pool: %w", err)
		}
		return struct{}{}, nil
	})
	return err
}
