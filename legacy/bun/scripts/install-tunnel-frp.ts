import process from 'node:process'
import { ensureFrpBinary } from '../src/commands/tunnel/frp/binary'

const architecture = process.argv[2]
if (architecture !== 'x64' && architecture !== 'arm64')
  throw new Error(`Expected x64 or arm64 architecture, received: ${architecture ?? '<missing>'}`)

// Cross-architecture Docker builds cannot execute the downloaded binary here.
await ensureFrpBinary('frps', {
  platform: 'linux',
  architecture,
  verifyVersion: async () => {},
})
