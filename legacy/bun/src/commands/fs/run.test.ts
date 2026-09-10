import type { NetworkInterfaceInfo } from 'node:os'
import { describe, expect, test } from 'bun:test'
import { formatFsUrls } from './run'

const interfaces: NodeJS.Dict<NetworkInterfaceInfo[]> = {
  loopback: [{ address: '127.0.0.1', netmask: '255.0.0.0', family: 'IPv4', mac: '', internal: true, cidr: '127.0.0.1/8' }],
  network: [{ address: '192.168.1.25', netmask: '255.255.255.0', family: 'IPv4', mac: '', internal: false, cidr: '192.168.1.25/24' }],
}

describe('formatFsUrls', () => {
  test('prints localhost and LAN URLs for the default public binding', () => {
    expect(formatFsUrls('0.0.0.0', '1204', interfaces)).toEqual([
      { label: 'Local', url: 'http://localhost:1204' },
      { label: 'Network', url: 'http://192.168.1.25:1204' },
    ])
  })

  test('prints only the selected address for a manual binding', () => {
    expect(formatFsUrls('127.0.0.1', '43123', interfaces)).toEqual([
      { label: 'Local', url: 'http://127.0.0.1:43123' },
    ])
  })
})
