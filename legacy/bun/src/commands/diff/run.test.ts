import type { NetworkInterfaceInfo } from 'node:os'
import { describe, expect, test } from 'bun:test'
import { formatDiffUrls } from './run'

const interfaces: Record<string, NetworkInterfaceInfo[]> = {
  en0: [
    {
      address: '192.168.1.50',
      netmask: '255.255.255.0',
      family: 'IPv4',
      mac: '00:11:22:33:44:55',
      internal: false,
      cidr: '192.168.1.50/24',
    },
    {
      address: 'fe80::1',
      netmask: 'ffff:ffff:ffff:ffff::',
      family: 'IPv6',
      mac: '00:11:22:33:44:55',
      internal: false,
      cidr: 'fe80::1/64',
      scopeid: 0,
    },
  ],
  lo0: [{
    address: '127.0.0.1',
    netmask: '255.0.0.0',
    family: 'IPv4',
    mac: '00:00:00:00:00:00',
    internal: true,
    cidr: '127.0.0.1/8',
  }],
}

describe('formatDiffUrls', () => {
  test('prints only the loopback URL for local access', () => {
    expect(formatDiffUrls(false, '43123', interfaces)).toEqual([
      'http://127.0.0.1:43123',
    ])
  })

  test('prints the local URL and non-internal IPv4 URLs for public access', () => {
    expect(formatDiffUrls(true, '43123', interfaces)).toEqual([
      'http://localhost:43123',
      'http://192.168.1.50:43123',
    ])
  })
})
