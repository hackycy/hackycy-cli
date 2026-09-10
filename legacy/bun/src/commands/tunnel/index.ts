import type { Command } from 'commander'
import type { ClientOptionInput, ServerOptionInput } from './config'
import process from 'node:process'
import { rememberTunnelConnection } from '../../config/tunnel'
import { getLogger } from '../../shared/log'
import { maskTunnelToken, resolveClientConfig, resolveServerConfig } from './config'

export function register(program: Command): void {
  const tunnel = program.command('tunnel').description('Manage trusted tunnel clients and tunnel definitions')

  tunnel
    .command('server')
    .description('Run the Tunnel Control Plane and supervised frps process')
    .option('--address <address>', 'Address for all tunnel server listeners')
    .option('--control-port <port>', 'Tunnel Control Plane port')
    .option('--frp-port <port>', 'FRP bind port')
    .option('--http-port <port>', 'FRP HTTP vhost port')
    .option('--port-range <start-end>', 'Server Port Pool')
    .option('--advertise-frp-addr <host:port>', 'FRP endpoint advertised to trusted clients')
    .option('--data-dir <path>', 'Tunnel server state directory')
    .option('--session-idle-days <days>', 'Session idle lifetime in days')
    .action(async (options: ServerOptionInput) => {
      const { runTunnelServer } = await import('./server/run')
      const logger = getLogger('tunnel.server')
      let config: ReturnType<typeof resolveServerConfig>
      try {
        config = resolveServerConfig(options)
      }
      catch (cause) {
        logger.error('Could not resolve tunnel server configuration', cause)
        throw cause
      }
      await runTunnelServer(config)
    })

  tunnel
    .command('connect')
    .description('Connect a native trusted client to a Tunnel Control Plane')
    .option('--server <control-plane>', 'Tunnel Control Plane origin')
    .option('--token <client-token>', 'Client Token')
    .action(async (options: ClientOptionInput) => {
      const { runTunnelClient } = await import('./client/run')
      const logger = getLogger('tunnel.client')
      const interactive = Boolean(process.stdin.isTTY && process.stdout.isTTY)
      let resolved: Awaited<ReturnType<typeof resolveClientConfig>>
      try {
        resolved = await resolveClientConfig(options, process.env, undefined, {
          selectConnection: interactive
            ? async (connections) => {
              const prompts = await import('@clack/prompts')
              const selected = await prompts.select({
                message: 'Select a tunnel connection',
                options: connections.map(connection => ({
                  value: connection.id,
                  label: `${connection.server}  ${maskTunnelToken(connection.token)}`,
                })),
              })
              if (prompts.isCancel(selected)) {
                prompts.cancel('Cancelled')
                return undefined
              }
              return selected
            }
            : undefined,
        })
      }
      catch (cause) {
        logger.error('Could not resolve tunnel client configuration', cause)
        throw cause
      }
      if (!resolved)
        return
      await runTunnelClient(resolved.config, {
        onAuthenticated: resolved.rememberOnAuthentication
          ? () => rememberTunnelConnection(resolved.config)
          : undefined,
      })
    })
}
