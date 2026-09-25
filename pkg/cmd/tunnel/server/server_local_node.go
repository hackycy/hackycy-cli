package server

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
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
		var port int
		err := connection.QueryRowContext(ctx, `
			SELECT server_port FROM tunnels WHERE node_id = 'local'
			  AND protocol IN ('tcp', 'udp') AND (server_port < ? OR server_port > ?)
			LIMIT 1
		`, pool.Start, pool.End).Scan(&port)
		if err == nil {
			return struct{}{}, fmt.Errorf("Local Node port pool %d-%d excludes occupied port %d", pool.Start, pool.End, port)
		}
		if err != sql.ErrNoRows {
			return struct{}{}, fmt.Errorf("check Local Node port pool: %w", err)
		}
		var frpHost, frpPort, httpHost, httpPort any
		if settings.AdvertiseFRPAddr != nil {
			frpHost = strings.ToLower(settings.AdvertiseFRPAddr.Host)
			frpPort = settings.AdvertiseFRPAddr.Port
			httpHost = frpHost
			httpPort = settings.HTTPPort
		} else if settings.Address != "" && settings.Address != "0.0.0.0" && settings.Address != "::" {
			frpHost = strings.ToLower(settings.Address)
			frpPort = settings.FRPPort
			httpHost = frpHost
			httpPort = settings.HTTPPort
		}
		if _, err := connection.ExecContext(ctx, `
			UPDATE nodes SET advertised_frp_host = ?, advertised_frp_port = ?,
			  http_ingress_host = ?, http_ingress_port = ?, updated_at = datetime('now')
			WHERE node_id = 'local'
		`, frpHost, frpPort, httpHost, httpPort); err != nil {
			return struct{}{}, fmt.Errorf("project Local Node endpoint: %w", err)
		}
		if _, err := connection.ExecContext(ctx, `
			INSERT INTO node_port_pools(node_id, port_start, port_end) VALUES('local', ?, ?)
			ON CONFLICT(node_id) DO UPDATE SET port_start = excluded.port_start, port_end = excluded.port_end
		`, pool.Start, pool.End); err != nil {
			return struct{}{}, fmt.Errorf("project Local Node port pool: %w", err)
		}
		return struct{}{}, nil
	})
	return err
}
